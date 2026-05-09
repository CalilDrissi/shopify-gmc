package web

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// logCLIFailure records the full CLI invocation + combined output so the
// prod log captures the root cause when a sudo /usr/local/bin/mailbox call
// returns non-zero. Without this we only ever see "exit status 1" in the
// user-facing banner with no detail.
func logCLIFailure(r *http.Request, op string, args []string, out string, err error) {
	slog.ErrorContext(r.Context(), "mail_cli_failure",
		"op", op,
		"args", strings.Join(args, " "),
		"err", err.Error(),
		"output", strings.TrimSpace(out),
	)
}

// trimErrAndOut formats the user-facing banner payload for a CLI failure.
// Combines the Go error string ("exit status 1") with whatever stdout/stderr
// the CLI emitted, separated by " — ", trimmed to 400 chars.
func trimErrAndOut(err error, out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return trimOut(err.Error())
	}
	// Replace internal newlines with " · " so the URL-encoded payload
	// stays single-line and renders cleanly in the banner.
	out = strings.ReplaceAll(out, "\r", "")
	out = strings.ReplaceAll(out, "\n", " · ")
	return trimOut(err.Error() + " — " + out)
}

// MailAdmin paths. The wrapper script lives at /usr/local/bin/mailbox
// (installed by deploy/webmail.sh on the production box). On dev hosts
// without it the page renders read-only with a "not configured" notice
// — no surface change, just guarded buttons.
const (
	mailboxBinary = "/usr/local/bin/mailbox"
	dovecotUsers  = "/etc/dovecot/users"
	postfixAlias  = "/etc/postfix/virtual"
	vmailBase     = "/var/mail/vmail"
	defaultQuota  = "1G" // mirrors deploy/mailbox DEFAULT_QUOTA
	// Bulk import caps. 1000 rows is enough for any realistic team
	// provision; 8 MiB upload is plenty for that many rows of
	// `email,password,quota` (~80 chars each).
	maxImportRows      = 1000
	maxImportUploadMem = 8 << 20

	mailLogPath        = "/var/log/mail.log"
	mailLogTailMax     = int64(100 * 1024 * 1024) // read at most last 100 MiB
	mailActivityCap    = 200
	mailActivityCacheT = 5 * time.Second
)

var (
	emailRE = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	// quota size: digits + optional K/M/G/T, or "0", or empty (unlimited / default)
	quotaSizeRE = regexp.MustCompile(`^([0-9]+[KMGTkmgt]?)?$`)
	// extracts SIZE from "userdb_quota_rule=*:storage=1G" anywhere on the line
	quotaRuleRE = regexp.MustCompile(`userdb_quota_rule=\*:storage=([0-9]+[KMGTkmgt]?)`)
	// nopassword=y in the 8th-field extras → mailbox is suspended (auth rejected).
	suspendedRE = regexp.MustCompile(`(^|[\s:])nopassword=y(\s|$)`)
	// vacation* pull values back out of a sieve script we generated.
	vacationSubjectRE = regexp.MustCompile(`(?s):subject\s+"((?:\\.|[^"\\])*)"`)
	vacationBodyRE    = regexp.MustCompile(`(?s)"((?:\\.|[^"\\])*)"\s*;\s*\}?\s*$`)
	vacationStartRE   = regexp.MustCompile(`currentdate\s+:value\s+"ge"\s+"date"\s+"(\d{4}-\d{2}-\d{2})"`)
	vacationEndRE     = regexp.MustCompile(`currentdate\s+:value\s+"le"\s+"date"\s+"(\d{4}-\d{2}-\d{2})"`)
	isoDateRE         = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// thresholdRE allows a non-negative decimal (e.g. "8", "5.5") or empty/"none"
	// to clear. Anything else is rejected by MailSpamThreshold before shelling out.
	thresholdRE = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?)?$`)
)

// MailPage lists every mailbox + alias on the host. Admin-only.
//
// We read the source-of-truth files directly rather than shell out for the
// list (faster, no sudo needed for read). Mutations all go through `sudo
// mailbox` which the deploy user is granted NOPASSWD for via /etc/sudoers.d/mailbox.
func (h *AdminHandlers) MailPage(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	configured := mailboxConfigured()

	mailboxes, mailErr := readMailboxes()
	aliases, aliasErr := readAliases()

	d.Title = "Mail"
	d.Data = map[string]any{
		"Configured": configured,
		"Mailboxes":  mailboxes,
		"Aliases":    aliases,
		"Result":     mailResultFromQuery(r),
		"Error":      firstNonNilErrText(mailErr, aliasErr),
	}
	h.renderAdmin(w, r, "admin-mail", *d)
}

// MailAdd creates a new mailbox. If "password" is blank we let the CLI
// generate one; we surface whatever the CLI printed on the redirect page.
func (h *AdminHandlers) MailAdd(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	pw := r.FormValue("password")
	if !emailRE.MatchString(email) {
		mailRedirect(w, r, "error", "invalid email")
		return
	}
	args := []string{mailboxBinary, "add", email}
	if pw != "" {
		args = append(args, pw)
	}
	out, err := runSudo(args...)
	if err != nil {
		logCLIFailure(r, "add", args, out, err)
		mailRedirect(w, r, "error", trimErrAndOut(err, out))
		return
	}
	// CLI prints "password: <pw>" on the second line — surface it once.
	pwOut := extractCLIField(out, "password:")
	mailRedirect(w, r, "added", email+"|"+pwOut)
}

// MailPasswd sets a mailbox password to an admin-supplied value. The
// admin types the new password into the row's inline field; we hand it
// straight to the CLI's second positional arg (`mailbox passwd EMAIL PW`).
// We do not echo the password back in the banner since the admin already
// has it on screen.
func (h *AdminHandlers) MailPasswd(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	if !emailRE.MatchString(email) {
		mailRedirect(w, r, "error", "invalid email")
		return
	}
	pw := r.FormValue("password")
	if len(pw) < 8 {
		mailRedirect(w, r, "error", "password must be at least 8 characters")
		return
	}
	if len(pw) > 128 {
		mailRedirect(w, r, "error", "password too long (max 128)")
		return
	}
	args := []string{mailboxBinary, "passwd", email, pw}
	out, err := runSudo(args...)
	if err != nil {
		logCLIFailure(r, "passwd", args, out, err)
		mailRedirect(w, r, "error", trimErrAndOut(err, out))
		return
	}
	mailRedirect(w, r, "passwd", email)
}

// MailDel deletes a mailbox + its Maildir. The CLI normally prompts for
// confirmation; we pre-confirm by piping the email back over stdin so the
// CLI's `read` succeeds without a TTY.
func (h *AdminHandlers) MailDel(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	confirm := strings.TrimSpace(r.FormValue("confirm"))
	if !emailRE.MatchString(email) {
		mailRedirect(w, r, "error", "invalid email")
		return
	}
	if confirm != email {
		mailRedirect(w, r, "error", "type the full email to confirm deletion")
		return
	}
	cmd := exec.Command("sudo", "-n", mailboxBinary, "del", email)
	cmd.Stdin = strings.NewReader(email + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		logCLIFailure(r, "del", []string{mailboxBinary, "del", email}, string(out), err)
		mailRedirect(w, r, "error", trimErrAndOut(err, string(out)))
		return
	}
	mailRedirect(w, r, "deleted", email)
}

// MailAlias creates or replaces an alias.
func (h *AdminHandlers) MailAlias(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	from := strings.TrimSpace(strings.ToLower(r.FormValue("from")))
	to := strings.TrimSpace(r.FormValue("to"))
	if !emailRE.MatchString(from) {
		mailRedirect(w, r, "error", "from must be a valid email")
		return
	}
	if to == "" {
		mailRedirect(w, r, "error", "to required")
		return
	}
	aliasArgs := []string{mailboxBinary, "alias", from, to}
	if out, err := runSudo(aliasArgs...); err != nil {
		logCLIFailure(r, "alias", aliasArgs, out, err)
		mailRedirect(w, r, "error", trimErrAndOut(err, out))
		return
	}
	mailRedirect(w, r, "alias", from+"|"+to)
}

// MailUnalias removes an alias.
func (h *AdminHandlers) MailUnalias(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	from := strings.TrimSpace(strings.ToLower(r.FormValue("from")))
	if !emailRE.MatchString(from) {
		mailRedirect(w, r, "error", "from must be a valid email")
		return
	}
	unaliasArgs := []string{mailboxBinary, "unalias", from}
	if out, err := runSudo(unaliasArgs...); err != nil {
		logCLIFailure(r, "unalias", unaliasArgs, out, err)
		mailRedirect(w, r, "error", trimErrAndOut(err, out))
		return
	}
	mailRedirect(w, r, "unalias", from)
}

// MailQuota sets a per-mailbox storage quota. Empty size clears the
// override (falls back to the system default); "0" means unlimited.
// Anything else must match `quotaSizeRE` (e.g. "1G", "500M", "2K").
func (h *AdminHandlers) MailQuota(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	size := strings.TrimSpace(r.FormValue("size"))
	if !emailRE.MatchString(email) {
		mailRedirect(w, r, "error", "invalid email")
		return
	}
	if !quotaSizeRE.MatchString(size) {
		mailRedirect(w, r, "error", "bad size (use 1G, 500M, 2K, 0=unlimited, or empty=default)")
		return
	}
	args := []string{mailboxBinary, "quota", email}
	if size != "" {
		args = append(args, size)
	}
	if out, err := runSudo(args...); err != nil {
		logCLIFailure(r, "quota", args, out, err)
		mailRedirect(w, r, "error", trimErrAndOut(err, out))
		return
	}
	display := size
	if display == "" {
		display = "default"
	}
	mailRedirect(w, r, "quota", email+"|"+display)
}

// MailSuspend toggles a mailbox's auth state. Form param `action` selects
// `suspend` (auth rejected, mail still delivers) or `unsuspend` (auth allowed
// again). The CLI does the in-place edit + doveadm reload; we just dispatch.
func (h *AdminHandlers) MailSuspend(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	action := strings.TrimSpace(r.FormValue("action"))
	if !emailRE.MatchString(email) {
		mailRedirect(w, r, "error", "invalid email")
		return
	}
	if action != "suspend" && action != "unsuspend" {
		mailRedirect(w, r, "error", "action must be suspend or unsuspend")
		return
	}
	suspArgs := []string{mailboxBinary, action, email}
	if out, err := runSudo(suspArgs...); err != nil {
		logCLIFailure(r, action, suspArgs, out, err)
		mailRedirect(w, r, "error", trimErrAndOut(err, out))
		return
	}
	mailRedirect(w, r, action, email)
}

// MailVacationGet renders the per-mailbox vacation editor.
func (h *AdminHandlers) MailVacationGet(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("email")))
	if !emailRE.MatchString(email) {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}
	state := readVacationState(email)
	d.Title = "Vacation · " + email
	d.Data = map[string]any{
		"Configured": mailboxConfigured(),
		"Email":      email,
		"State":      state,
	}
	h.renderAdmin(w, r, "admin-mail-vacation", *d)
}

// MailVacationSave writes a new vacation.sieve, then enables or disables it.
func (h *AdminHandlers) MailVacationSave(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	subject := strings.TrimSpace(r.FormValue("subject"))
	body := r.FormValue("body")
	start := strings.TrimSpace(r.FormValue("start"))
	end := strings.TrimSpace(r.FormValue("end"))
	enable := r.FormValue("enabled") != ""

	if !emailRE.MatchString(email) {
		mailRedirect(w, r, "error", "invalid email")
		return
	}
	if subject == "" {
		mailRedirect(w, r, "error", "subject required")
		return
	}
	if strings.TrimSpace(body) == "" {
		mailRedirect(w, r, "error", "body required")
		return
	}
	if start != "" && !isoDateRE.MatchString(start) {
		mailRedirect(w, r, "error", "start must be YYYY-MM-DD")
		return
	}
	if end != "" && !isoDateRE.MatchString(end) {
		mailRedirect(w, r, "error", "end must be YYYY-MM-DD")
		return
	}
	if (start == "") != (end == "") {
		mailRedirect(w, r, "error", "start and end must be set together (or both empty)")
		return
	}

	args := []string{mailboxBinary, "vacation", email, "set", subject, body}
	if start != "" {
		args = append(args, start, end)
	}
	if out, err := runSudo(args...); err != nil {
		mailRedirect(w, r, "error", trimOut(err.Error()+"\n"+out))
		return
	}
	verb := "disable"
	kind := "vacation-off"
	if enable {
		verb = "enable"
		kind = "vacation-on"
	}
	if out, err := runSudo(mailboxBinary, "vacation", email, verb); err != nil {
		mailRedirect(w, r, "error", trimOut(err.Error()+"\n"+out))
		return
	}
	mailRedirect(w, r, kind, email)
}

// MailActivity renders the last ~200 mail.log entries that touched the given
// mailbox (as sender or recipient). Pure read; no side effects.
func (h *AdminHandlers) MailActivity(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("email")))
	if !emailRE.MatchString(email) {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}

	events, err := mailActivityFor(email)

	d.Title = "Mail activity"
	d.Data = map[string]any{
		"Email":  email,
		"Events": events,
		"Error":  errText(err),
	}
	h.renderAdmin(w, r, "admin-mail-activity", *d)
}

// MailActivityEvent is one reconstructed Postfix mail-flow record. A record
// can collect multiple log lines that share the same Postfix queue ID — we
// keep the timestamp from the first line that sets a from/to and the status
// from the line that announces the final disposition (sent/bounced/deferred).
type MailActivityEvent struct {
	Timestamp time.Time
	QueueID   string
	From      string
	To        string
	SizeBytes int64
	Status    string // "sent" | "bounced" | "deferred" | ""
	DSN       string // e.g. "5.2.2"
	// Direction: "in" if `email` is the recipient, "out" if it's the sender.
	Direction string
	// Counterparty is the other side of the flow from `email`'s perspective.
	Counterparty string
}

// in-process cache: 5s TTL keyed by email so a refresh doesn't re-parse the
// log. Map size is bounded — entries are evicted lazily on read when stale.
var (
	mailActivityCacheMu sync.Mutex
	mailActivityCache   = map[string]mailActivityCacheEntry{}
)

type mailActivityCacheEntry struct {
	at     time.Time
	events []MailActivityEvent
	err    error
}

// mailActivityFor wraps the file IO + parser + cache. The parser is split out
// (parseMailLog) so it can be unit-tested with fixture lines.
func mailActivityFor(email string) ([]MailActivityEvent, error) {
	mailActivityCacheMu.Lock()
	if e, ok := mailActivityCache[email]; ok && time.Since(e.at) < mailActivityCacheT {
		mailActivityCacheMu.Unlock()
		return e.events, e.err
	}
	mailActivityCacheMu.Unlock()

	lines, err := readMailLogTail(mailLogPath, mailLogTailMax)
	if err != nil {
		mailActivityCacheMu.Lock()
		mailActivityCache[email] = mailActivityCacheEntry{at: time.Now(), err: err}
		mailActivityCacheMu.Unlock()
		return nil, err
	}
	events := parseMailLog(lines, email, mailActivityCap)

	mailActivityCacheMu.Lock()
	mailActivityCache[email] = mailActivityCacheEntry{at: time.Now(), events: events}
	mailActivityCacheMu.Unlock()
	return events, nil
}

// readMailLogTail reads the file at path, capping memory by seeking to the
// last `maxBytes` if the file is larger. Returns lines (no trailing newline).
// If the file is missing the caller gets an empty slice + the error so the
// page can render a "no log accessible" notice.
func readMailLogTail(path string, maxBytes int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > maxBytes {
		if _, err := f.Seek(st.Size()-maxBytes, io.SeekStart); err != nil {
			return nil, err
		}
	}
	br := bufio.NewReader(f)
	// First (possibly partial) line after a Seek is dropped — we don't know
	// where the previous newline was. Subsequent lines are clean.
	dropPartial := st.Size() > maxBytes
	var lines []string
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			if dropPartial {
				dropPartial = false
			} else {
				lines = append(lines, strings.TrimRight(line, "\r\n"))
			}
		}
		if err != nil {
			break
		}
	}
	return lines, nil
}

// Postfix log lines come in two header flavours:
//   syslog:        "May  9 12:34:56 host postfix/qmgr[123]: ABC123: from=<x>, ..."
//   rfc3339-ish:   "2026-05-09T12:34:56.789012+00:00 host postfix/smtpd[123]: ABC123: ..."
// We accept both. The component (`postfix/qmgr`, `postfix/smtpd`, ...) is
// matched loosely; only `postfix/*` lines are interesting.
var (
	mailLogHeadRE = regexp.MustCompile(`^(\S+(?:\s+\S+\s+\S+)?)\s+\S+\s+postfix/[A-Za-z0-9._-]+\[\d+\]:\s+(.*)$`)
	queueIDRE     = regexp.MustCompile(`^([A-F0-9]{8,})\s*:\s*(.*)$`)
	fromRE        = regexp.MustCompile(`from=<([^>]*)>`)
	toRE          = regexp.MustCompile(`to=<([^>]*)>`)
	sizeRE        = regexp.MustCompile(`size=(\d+)`)
	statusRE      = regexp.MustCompile(`status=(\w+)`)
	dsnRE         = regexp.MustCompile(`dsn=(\d+\.\d+\.\d+)`)
)

// parseMailLog groups Postfix log lines by queue ID, filters down to events
// where `email` is the sender or any recipient, and returns up to `cap`
// events sorted descending by timestamp. Pure function — no IO.
func parseMailLog(lines []string, email string, cap int) []MailActivityEvent {
	if email == "" {
		return nil
	}
	emailLower := strings.ToLower(email)
	now := time.Now()

	type acc struct {
		ts     time.Time
		from   string
		to     []string
		size   int64
		status string
		dsn    string
	}
	byQID := map[string]*acc{}
	order := []string{} // first-seen order, used as a stable tiebreaker

	for _, line := range lines {
		m := mailLogHeadRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ts := parseMailLogTimestamp(m[1], now)
		body := m[2]
		qm := queueIDRE.FindStringSubmatch(body)
		if qm == nil {
			continue
		}
		qid := qm[1]
		rest := qm[2]
		a, ok := byQID[qid]
		if !ok {
			a = &acc{ts: ts}
			byQID[qid] = a
			order = append(order, qid)
		} else if a.ts.IsZero() && !ts.IsZero() {
			a.ts = ts
		}
		if mm := fromRE.FindStringSubmatch(rest); mm != nil && a.from == "" {
			a.from = strings.ToLower(mm[1])
		}
		if mm := toRE.FindStringSubmatch(rest); mm != nil {
			to := strings.ToLower(mm[1])
			if !containsString(a.to, to) {
				a.to = append(a.to, to)
			}
		}
		if mm := sizeRE.FindStringSubmatch(rest); mm != nil && a.size == 0 {
			fmt.Sscanf(mm[1], "%d", &a.size)
		}
		if mm := statusRE.FindStringSubmatch(rest); mm != nil {
			a.status = mm[1]
			a.ts = ts // disposition line wins for the displayed timestamp
		}
		if mm := dsnRE.FindStringSubmatch(rest); mm != nil {
			a.dsn = mm[1]
		}
	}

	var out []MailActivityEvent
	for _, qid := range order {
		a := byQID[qid]
		isSender := a.from == emailLower
		var matchTo string
		for _, t := range a.to {
			if t == emailLower {
				matchTo = t
				break
			}
		}
		if !isSender && matchTo == "" {
			continue
		}
		ev := MailActivityEvent{
			Timestamp: a.ts,
			QueueID:   qid,
			From:      a.from,
			SizeBytes: a.size,
			Status:    a.status,
			DSN:       a.dsn,
		}
		if len(a.to) > 0 {
			ev.To = a.to[0]
		}
		if isSender {
			ev.Direction = "out"
			if len(a.to) > 0 {
				ev.Counterparty = a.to[0]
			}
		} else {
			ev.Direction = "in"
			ev.Counterparty = a.from
		}
		out = append(out, ev)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if len(out) > cap {
		out = out[:cap]
	}
	return out
}

// parseMailLogTimestamp accepts both the syslog-style "May  9 12:34:56" stamp
// (no year — we infer it from `now`, rolling backward when the parsed month is
// in the future) and the rfc3339-ish stamp Postfix emits when systemd's
// journalctl is the log source. Returns zero on any parse failure.
func parseMailLogTimestamp(s string, now time.Time) time.Time {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// syslog: "May  9 12:34:56" — lacks a year.
	if t, err := time.Parse("Jan _2 15:04:05", s); err == nil {
		t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
		if t.After(now.Add(24 * time.Hour)) {
			t = t.AddDate(-1, 0, 0)
		}
		return t
	}
	return time.Time{}
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// mailboxConfigured returns true if the mailbox CLI is installed and
// executable. Lets the page render gracefully on dev hosts.
func mailboxConfigured() bool {
	st, err := os.Stat(mailboxBinary)
	return err == nil && !st.IsDir() && st.Mode().Perm()&0111 != 0
}

// runSudo invokes `sudo -n <args...>` and returns combined output.
// `-n` prevents any password prompt — failure to exec without one is a
// configuration error caught immediately rather than hanging the request.
func runSudo(args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("runSudo: no args")
	}
	cmd := exec.Command("sudo", append([]string{"-n"}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Mailbox is one row in /etc/dovecot/users with disk usage joined in.
// UsedBytes comes from the maildir's `maildirsize` file (Dovecot writes
// it on every delivery / quota recalc — read is one stat + small file
// read, so listing every mailbox stays cheap).
type Mailbox struct {
	Email      string
	UsedBytes  int64
	QuotaBytes int64 // 0 means unlimited
	Suspended  bool
	Vacation   MailVacationState
}

// MailVacationState mirrors the parsed contents of vacation.sieve plus a
// flag for whether the active-script symlink currently points at it.
type MailVacationState struct {
	Enabled bool
	Subject string
	Body    string
	Start   string
	End     string
}

// readMailboxes parses /etc/dovecot/users (one mailbox per line, fields
// colon-separated). For each row we also resolve the per-mailbox quota
// override (8th-field `userdb_quota_rule=*:storage=<size>`, falling
// back to defaultQuota) and the current usage from `maildirsize`.
func readMailboxes() ([]Mailbox, error) {
	f, err := os.Open(dovecotUsers)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Mailbox
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		email := line[:i]
		size := defaultQuota
		if m := quotaRuleRE.FindStringSubmatch(line); len(m) == 2 {
			size = m[1]
		}
		quotaBytes, _ := parseSizeBytes(size)
		used := maildirsizeUsedBytes(maildirsizePath(email))
		out = append(out, Mailbox{
			Email:      email,
			UsedBytes:  used,
			QuotaBytes: quotaBytes,
			Suspended:  suspendedRE.MatchString(line),
			Vacation:   readVacationState(email),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, s.Err()
}

// maildirsizePath maps "user@domain" → "/var/mail/vmail/domain/user/maildirsize".
func maildirsizePath(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	return vmailBase + "/" + email[at+1:] + "/" + email[:at] + "/maildirsize"
}

// parseSizeBytes parses Dovecot quota sizes ("1G", "500M", "2K", "0", "").
// Empty or "0" → 0 (unlimited).
func parseSizeBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	mult := int64(1)
	last := s[len(s)-1]
	switch last {
	case 'k', 'K':
		mult = 1024
		s = s[:len(s)-1]
	case 'm', 'M':
		mult = 1024 * 1024
		s = s[:len(s)-1]
	case 'g', 'G':
		mult = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	case 't', 'T':
		mult = 1024 * 1024 * 1024 * 1024
		s = s[:len(s)-1]
	}
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	return n * mult, nil
}

// maildirsizeUsedBytes parses Dovecot's maildirsize file format:
//
//	<quota_bytes>S[<quota_count>C]\n     ← line 1: quota header (NOT usage)
//	<bytes> <msgs>\n                     ← line 2+: absolute baseline written
//	                                       by `doveadm quota recalc`, OR
//	+<add_bytes> <add_msgs>\n            ← signed deltas appended on each
//	-<sub_bytes> <sub_msgs>\n              delivery / expunge
//
// We skip line 1 entirely and sum the leading numeric token from every
// subsequent line. Returns the running sum (clamped at 0) so transient
// underflows during heavy expunge don't render negative usage.
//
// Missing or unreadable file → 0. A brand-new mailbox simply hasn't had a
// maildirsize written yet (no deliveries), which is the right answer.
func maildirsizeUsedBytes(path string) int64 {
	if path == "" {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	var total int64
	s := bufio.NewScanner(f)
	first := true
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		if first {
			first = false
			continue // quota header — skip, NOT a usage delta
		}
		// Parse leading signed integer ("+1234", "-5678", or "1234").
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		var n int64
		fmt.Sscanf(parts[0], "%d", &n)
		total += n
	}
	if total < 0 {
		return 0
	}
	return total
}

// vacationSievePath / vacationActivePath map an email to the on-disk
// paths the mailbox CLI writes. We read these directly (cheap stat +
// open) rather than shelling out for status — same pattern as the
// quota reader.
func vacationSievePath(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	return vmailBase + "/" + email[at+1:] + "/" + email[:at] + "/sieve/vacation.sieve"
}

func vacationActivePath(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	return vmailBase + "/" + email[at+1:] + "/" + email[:at] + "/.dovecot.sieve"
}

// readVacationState parses sieve/vacation.sieve (if it exists) and
// reports whether .dovecot.sieve is the active symlink. Missing files
// produce a zero state — caller treats that as "no vacation configured".
func readVacationState(email string) MailVacationState {
	out := MailVacationState{}
	scriptPath := vacationSievePath(email)
	activePath := vacationActivePath(email)
	if scriptPath == "" {
		return out
	}
	if _, err := os.Lstat(activePath); err == nil {
		out.Enabled = true
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return out
	}
	out.Subject, out.Body, out.Start, out.End = parseVacationSieve(string(data))
	return out
}

// parseVacationSieve extracts subject / body / start / end from a sieve
// script we generated. Body is the last quoted string before the trailing
// `;` of the vacation action (which our generator always places at end of
// the file, optionally inside a date-guard `if {...}` block).
func parseVacationSieve(s string) (subject, body, start, end string) {
	if m := vacationSubjectRE.FindStringSubmatch(s); len(m) == 2 {
		subject = unescapeSieveString(m[1])
	}
	if m := vacationBodyRE.FindStringSubmatch(strings.TrimRight(s, " \t\r\n")); len(m) == 2 {
		body = unescapeSieveString(m[1])
	}
	if m := vacationStartRE.FindStringSubmatch(s); len(m) == 2 {
		start = m[1]
	}
	if m := vacationEndRE.FindStringSubmatch(s); len(m) == 2 {
		end = m[1]
	}
	return
}

// unescapeSieveString reverses the `\\` and `\"` escapes the CLI applies.
// Anything else is treated as literal — Sieve does not have C-style
// numeric escapes.
func unescapeSieveString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// MailAlias is a from→to pair.
type MailAlias struct{ From, To string }

func readAliases() ([]MailAlias, error) {
	f, err := os.Open(postfixAlias)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []MailAlias
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		out = append(out, MailAlias{From: parts[0], To: parts[1]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out, s.Err()
}

// mailRedirect performs a Post/Redirect/Get with a `result` query carrying
// "<kind>|<payload>" the GET handler unpacks into a banner.
func mailRedirect(w http.ResponseWriter, r *http.Request, kind, payload string) {
	q := fmt.Sprintf("?result=%s&payload=%s", kind, urlEscape(payload))
	http.Redirect(w, r, "/admin/mail"+q, http.StatusFound)
}

// mailResultFromQuery decodes the banner the redirect target reads.
func mailResultFromQuery(r *http.Request) map[string]string {
	kind := r.URL.Query().Get("result")
	payload := r.URL.Query().Get("payload")
	if kind == "" {
		return nil
	}
	out := map[string]string{"Kind": kind, "Payload": payload}
	// Split "<email>|<password>" payloads into separate fields for easy
	// templating. Raw payload also preserved.
	if i := strings.Index(payload, "|"); i >= 0 {
		out["Email"] = payload[:i]
		out["Right"] = payload[i+1:]
	} else {
		out["Email"] = payload
	}
	return out
}

func urlEscape(s string) string {
	// Minimal escaping — the values we feed in are emails or plain ASCII
	// passwords. net/url.QueryEscape would also work; rolled here to
	// avoid one more import block.
	r := strings.NewReplacer("&", "%26", "+", "%2B", " ", "%20", "#", "%23", "?", "%3F")
	return r.Replace(s)
}

func extractCLIField(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if i := strings.Index(strings.ToLower(l), prefix); i >= 0 {
			return strings.TrimSpace(l[i+len(prefix):])
		}
	}
	return ""
}

func trimOut(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func firstNonNilErrText(errs ...error) string {
	for _, e := range errs {
		if e != nil {
			return e.Error()
		}
	}
	return ""
}

// ----------------------------------------------------------------------------
// Bulk CSV import
// ----------------------------------------------------------------------------

// ImportRow is a single parsed row from the upload CSV. Validation errors
// are stamped on `Err` so the per-row results table can show why a row was
// skipped without blowing up the whole batch.
type ImportRow struct {
	LineNo   int    // 1-based line in the CSV (header is line 1, first data row line 2)
	Email    string
	Password string
	Quota    string
	Err      string // empty = row passed pre-flight validation
}

// ImportResult is one row in the post-execute table the result page renders.
// `Status` is one of "created", "skipped:duplicate", "error:<reason>".
type ImportResult struct {
	LineNo   int
	Email    string
	Password string // generated-or-supplied; one-time display
	Quota    string
	Status   string
}

// parseImportCSV reads the upload, validates each row against emailRE +
// quotaSizeRE, and returns the parsed rows. The header row must be exactly
// `email,password,quota` (case-insensitive, trimmed). Returns an error only
// for problems that mean the upload is unusable (missing/wrong header,
// over the row cap). Per-row validation problems are stamped on
// ImportRow.Err so the caller can render them in the result table.
func parseImportCSV(r io.Reader) ([]ImportRow, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; we validate cell counts ourselves
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err == io.EOF {
		return nil, errors.New("empty CSV (no header row)")
	}
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if len(header) < 3 ||
		!strings.EqualFold(strings.TrimSpace(header[0]), "email") ||
		!strings.EqualFold(strings.TrimSpace(header[1]), "password") ||
		!strings.EqualFold(strings.TrimSpace(header[2]), "quota") {
		return nil, errors.New("CSV header must be `email,password,quota`")
	}

	var out []ImportRow
	lineNo := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		lineNo++
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		// Pad short records to 3 cells so trim/index never panics.
		for len(rec) < 3 {
			rec = append(rec, "")
		}
		email := strings.TrimSpace(strings.ToLower(rec[0]))
		password := strings.TrimSpace(rec[1])
		quota := strings.TrimSpace(rec[2])
		if email == "" && password == "" && quota == "" {
			continue // blank line in the middle of the CSV — skip silently
		}

		row := ImportRow{LineNo: lineNo, Email: email, Password: password, Quota: quota}
		switch {
		case !emailRE.MatchString(email):
			row.Err = "invalid email"
		case !quotaSizeRE.MatchString(quota):
			row.Err = "bad quota (use 1G, 500M, 2K, 0=unlimited, or empty=default)"
		}
		out = append(out, row)

		if len(out) > maxImportRows {
			return nil, fmt.Errorf("too many rows (max %d)", maxImportRows)
		}
	}
	return out, nil
}

// MailImportPage renders the upload form.
func (h *AdminHandlers) MailImportPage(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	d.Title = "Bulk import mailboxes"
	d.Data = map[string]any{
		"Configured": mailboxConfigured(),
		"MaxRows":    maxImportRows,
	}
	h.renderAdmin(w, r, "admin-mail-import", *d)
}

// MailImportSubmit accepts the CSV upload, runs `mailbox add` per valid row,
// and renders a per-row result table. Partial failures are surfaced; one bad
// row never aborts the rest. Generated passwords are shown once on this
// response — there is no second chance to retrieve them.
func (h *AdminHandlers) MailImportSubmit(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseMultipartForm(maxImportUploadMem); err != nil {
		http.Error(w, "bad upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	rows, parseErr := parseImportCSV(file)
	if parseErr != nil {
		d.Title = "Bulk import mailboxes"
		d.Data = map[string]any{
			"Configured": true,
			"MaxRows":    maxImportRows,
			"Error":      parseErr.Error(),
		}
		h.renderAdmin(w, r, "admin-mail-import", *d)
		return
	}

	results := make([]ImportResult, 0, len(rows))
	var created, skipped, errored int
	for _, row := range rows {
		res := ImportResult{LineNo: row.LineNo, Email: row.Email, Quota: row.Quota}
		if row.Err != "" {
			res.Status = "error:" + row.Err
			errored++
			results = append(results, res)
			continue
		}
		args := []string{mailboxBinary, "add", row.Email}
		if row.Password != "" {
			args = append(args, row.Password)
		}
		out, addErr := runSudo(args...)
		if addErr != nil {
			combined := strings.ToLower(out + " " + addErr.Error())
			if strings.Contains(combined, "exists") || strings.Contains(combined, "duplicate") {
				res.Status = "skipped:duplicate"
				skipped++
			} else {
				res.Status = "error:" + trimOut(addErr.Error()+"\n"+out)
				errored++
			}
			results = append(results, res)
			continue
		}
		if row.Password != "" {
			res.Password = row.Password
		} else {
			res.Password = extractCLIField(out, "password:")
		}
		if row.Quota != "" {
			if _, qErr := runSudo(mailboxBinary, "quota", row.Email, row.Quota); qErr != nil {
				// Mailbox got created but quota set failed — surface as a
				// warning rather than a hard error so the operator can
				// retry the quota step from the main page.
				res.Status = "created (quota failed: " + trimOut(qErr.Error()) + ")"
				created++
				results = append(results, res)
				continue
			}
		}
		res.Status = "created"
		created++
		results = append(results, res)
	}

	d.Title = "Bulk import results"
	d.Data = map[string]any{
		"Results": results,
		"Total":   len(results),
		"Created": created,
		"Skipped": skipped,
		"Errored": errored,
	}
	h.renderAdmin(w, r, "admin-mail-import-result", *d)
}

// ============================================================================
// Phase 5 — Sieve filter rules
// ============================================================================

// FilterRule mirrors one row from `mailbox filter-list`. ID is 1-based and
// the index sent back to `mailbox filter-del`.
type FilterRule struct {
	ID     int
	Field  string
	Value  string
	Action string
	Arg    string
}

var (
	// fieldRE: from | subject | header:X-NAME (header name is RFC-ish ASCII).
	filterFieldRE = regexp.MustCompile(`^(from|subject|header:[A-Za-z0-9-]+)$`)
	// actionRE: one of the four actions our CLI knows how to render.
	filterActionRE = regexp.MustCompile(`^(move|tag|forward|discard)$`)
)

// MailFilters renders the per-mailbox filter rules page. Lists current
// rules (parsed from `mailbox filter-list EMAIL`'s TSV) and renders the
// add-rule form.
func (h *AdminHandlers) MailFilters(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("email")))
	if !emailRE.MatchString(email) {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}

	configured := mailboxConfigured()
	var rules []FilterRule
	var listErr string
	if configured {
		out, err := runSudo(mailboxBinary, "filter-list", email)
		if err != nil {
			listErr = trimOut(err.Error() + "\n" + out)
		} else {
			rules = parseFilterListTSV(out)
		}
	}

	d.Title = "Mail filters"
	d.Data = map[string]any{
		"Configured": configured,
		"Email":      email,
		"Rules":      rules,
		"Result":     mailResultFromQuery(r),
		"Error":      listErr,
	}
	h.renderAdmin(w, r, "admin-mail-filters", *d)
}

// MailFilterAdd prepends a rule to the mailbox's filters file.
func (h *AdminHandlers) MailFilterAdd(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	field := strings.TrimSpace(r.FormValue("field"))
	value := strings.TrimSpace(r.FormValue("value"))
	action := strings.TrimSpace(r.FormValue("action"))
	arg := strings.TrimSpace(r.FormValue("arg"))
	if !emailRE.MatchString(email) {
		filterRedirect(w, r, email, "error", "invalid email")
		return
	}
	if !filterFieldRE.MatchString(field) {
		filterRedirect(w, r, email, "error", "field must be from, subject, or header:X-NAME")
		return
	}
	if !filterActionRE.MatchString(action) {
		filterRedirect(w, r, email, "error", "action must be move, tag, forward, or discard")
		return
	}
	if value == "" {
		filterRedirect(w, r, email, "error", "value is required")
		return
	}
	if action != "discard" && arg == "" {
		filterRedirect(w, r, email, "error", "argument is required for "+action)
		return
	}
	if action == "forward" && !emailRE.MatchString(arg) {
		filterRedirect(w, r, email, "error", "forward target must be a valid email")
		return
	}
	if _, err := runSudo(mailboxBinary, "filter-add", email, field, value, action, arg); err != nil {
		filterRedirect(w, r, email, "error", trimOut(err.Error()))
		return
	}
	filterRedirect(w, r, email, "filter-add", action)
}

// MailFilterDel removes a rule by 1-based index.
func (h *AdminHandlers) MailFilterDel(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	id := strings.TrimSpace(r.FormValue("id"))
	if !emailRE.MatchString(email) {
		filterRedirect(w, r, email, "error", "invalid email")
		return
	}
	if matched, _ := regexp.MatchString(`^[0-9]+$`, id); !matched {
		filterRedirect(w, r, email, "error", "bad id")
		return
	}
	if _, err := runSudo(mailboxBinary, "filter-del", email, id); err != nil {
		filterRedirect(w, r, email, "error", trimOut(err.Error()))
		return
	}
	filterRedirect(w, r, email, "filter-del", id)
}

// parseFilterListTSV parses the TSV from `mailbox filter-list` —
// `id<TAB>field<TAB>value<TAB>action<TAB>arg` per line. Rows with too few
// fields are silently dropped to keep the page renderable even if the CLI
// version drifts.
func parseFilterListTSV(out string) []FilterRule {
	var rules []FilterRule
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		var id int
		fmt.Sscanf(parts[0], "%d", &id)
		arg := ""
		if len(parts) >= 5 {
			arg = parts[4]
		}
		rules = append(rules, FilterRule{
			ID:     id,
			Field:  parts[1],
			Value:  parts[2],
			Action: parts[3],
			Arg:    arg,
		})
	}
	return rules
}

// filterRedirect mirrors mailRedirect but stays on the per-mailbox filters
// page rather than the global mail page.
func filterRedirect(w http.ResponseWriter, r *http.Request, email, kind, payload string) {
	q := fmt.Sprintf("?email=%s&result=%s&payload=%s", urlEscape(email), kind, urlEscape(payload))
	http.Redirect(w, r, "/admin/mail/filters"+q, http.StatusFound)
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// mailboxConfigured returns true if the mailbox CLI is installed and
// executable. Lets the page render gracefully on dev hosts.

// ============================================================================
// Phase 6 — Spam settings (whitelist/blacklist + threshold)
// ============================================================================

// SpamState mirrors the three lines printed by `mailbox spam-list`.
// Threshold is empty when unset (we render "none").
type SpamState struct {
	Whitelist []string
	Blacklist []string
	Threshold string
}

// MailSpamGet renders the per-mailbox spam settings page. Empties the form
// gracefully if rspamd isn't running or if `mailbox spam-list` has nothing
// to show yet. Threshold input is rendered disabled (with a banner) when
// rspamd is absent — the whitelist/blacklist still work standalone.
func (h *AdminHandlers) MailSpamGet(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("email")))
	if !emailRE.MatchString(email) {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}
	state := readSpamState(email)
	rspamd := rspamdRunning()

	d.Title = "Mail · Spam"
	d.Data = map[string]any{
		"Configured": mailboxConfigured(),
		"Email":      email,
		"State":      state,
		"Rspamd":     rspamd,
		"Result":     mailResultFromQuery(r),
	}
	h.renderAdmin(w, r, "admin-mail-spam", *d)
}

// MailSpamAdd appends an address to the mailbox's whitelist or blacklist.
func (h *AdminHandlers) MailSpamAdd(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	list := strings.TrimSpace(r.FormValue("list"))
	addr := strings.TrimSpace(strings.ToLower(r.FormValue("addr")))
	if !emailRE.MatchString(email) {
		spamRedirect(w, r, email, "error", "invalid mailbox email")
		return
	}
	if list != "whitelist" && list != "blacklist" {
		spamRedirect(w, r, email, "error", "list must be whitelist or blacklist")
		return
	}
	if addr == "" || strings.ContainsAny(addr, " \t\r\n,\"\\") {
		spamRedirect(w, r, email, "error", "invalid sender address")
		return
	}
	sub := "spam-allow"
	if list == "blacklist" {
		sub = "spam-block"
	}
	if _, err := runSudo(mailboxBinary, sub, email, addr); err != nil {
		spamRedirect(w, r, email, "error", trimOut(err.Error()))
		return
	}
	spamRedirect(w, r, email, "spam-add", list+"|"+addr)
}

// MailSpamDel removes an address from a list. Same validation as Add.
func (h *AdminHandlers) MailSpamDel(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	list := strings.TrimSpace(r.FormValue("list"))
	addr := strings.TrimSpace(strings.ToLower(r.FormValue("addr")))
	if !emailRE.MatchString(email) {
		spamRedirect(w, r, email, "error", "invalid mailbox email")
		return
	}
	if list != "whitelist" && list != "blacklist" {
		spamRedirect(w, r, email, "error", "list must be whitelist or blacklist")
		return
	}
	if addr == "" || strings.ContainsAny(addr, " \t\r\n,\"\\") {
		spamRedirect(w, r, email, "error", "invalid sender address")
		return
	}
	sub := "spam-unallow"
	if list == "blacklist" {
		sub = "spam-unblock"
	}
	if _, err := runSudo(mailboxBinary, sub, email, addr); err != nil {
		spamRedirect(w, r, email, "error", trimOut(err.Error()))
		return
	}
	spamRedirect(w, r, email, "spam-del", list+"|"+addr)
}

// MailSpamThreshold sets or clears the per-mailbox X-Spam-Score threshold.
// Allowed: empty / "none" / decimal like "8.0". Anything else is rejected.
func (h *AdminHandlers) MailSpamThreshold(w http.ResponseWriter, r *http.Request) {
	d := h.adminCtx(r)
	if d == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	if !mailboxConfigured() {
		http.Error(w, "mail not configured on this host", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	score := strings.TrimSpace(r.FormValue("score"))
	if !emailRE.MatchString(email) {
		spamRedirect(w, r, email, "error", "invalid mailbox email")
		return
	}
	if score != "none" && !thresholdRE.MatchString(score) {
		spamRedirect(w, r, email, "error", "bad threshold (use a number like 8.0, or none)")
		return
	}
	args := []string{mailboxBinary, "spam-threshold", email}
	if score == "" {
		args = append(args, "none")
	} else {
		args = append(args, score)
	}
	if _, err := runSudo(args...); err != nil {
		spamRedirect(w, r, email, "error", trimOut(err.Error()))
		return
	}
	display := score
	if display == "" || display == "none" {
		display = "none"
	}
	kind := "spam-threshold"
	if !rspamdRunning() {
		kind = "spam-threshold-norspamd"
	}
	spamRedirect(w, r, email, kind, display)
}

// readSpamState invokes `mailbox spam-list EMAIL` (read-only — no sudo
// needed since the script only opens files vmail can already read; we still
// route through sudo for symmetry with the writers and to keep one path).
// On any error we return an empty state — page still renders.
func readSpamState(email string) SpamState {
	if !mailboxConfigured() || !emailRE.MatchString(email) {
		return SpamState{}
	}
	out, err := runSudo(mailboxBinary, "spam-list", email)
	if err != nil {
		return SpamState{}
	}
	return parseSpamList(out)
}

func parseSpamList(out string) SpamState {
	var s SpamState
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "whitelist:"):
			s.Whitelist = splitCSV(strings.TrimPrefix(line, "whitelist:"))
		case strings.HasPrefix(line, "blacklist:"):
			s.Blacklist = splitCSV(strings.TrimPrefix(line, "blacklist:"))
		case strings.HasPrefix(line, "threshold:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "threshold:"))
			if v != "" && v != "none" {
				s.Threshold = v
			}
		}
	}
	return s
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// rspamdRunning reports whether rspamd appears to be active on this host.
// Used for the "threshold won't fire" banner. Best-effort only — we tolerate
// a missing systemctl, missing socket, anything non-fatal.
func rspamdRunning() bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", "rspamd")
	if err := cmd.Run(); err == nil {
		return true
	}
	// Pidfile fallback (some installs don't ship a unit file).
	for _, p := range []string{"/run/rspamd/rspamd.pid", "/var/run/rspamd/rspamd.pid"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

func spamRedirect(w http.ResponseWriter, r *http.Request, email, kind, payload string) {
	q := fmt.Sprintf("?email=%s&result=%s&payload=%s", urlEscape(email), kind, urlEscape(payload))
	http.Redirect(w, r, "/admin/mail/spam"+q, http.StatusFound)
}

// ----------------------------------------------------------------------------
// Helpers
