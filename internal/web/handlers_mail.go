package web

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

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
)

var (
	emailRE = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	// quota size: digits + optional K/M/G/T, or "0", or empty (unlimited / default)
	quotaSizeRE = regexp.MustCompile(`^([0-9]+[KMGTkmgt]?)?$`)
	// extracts SIZE from "userdb_quota_rule=*:storage=1G" anywhere on the line
	quotaRuleRE = regexp.MustCompile(`userdb_quota_rule=\*:storage=([0-9]+[KMGTkmgt]?)`)
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
		mailRedirect(w, r, "error", trimOut(err.Error()+"\n"+out))
		return
	}
	// CLI prints "password: <pw>" on the second line — surface it once.
	pwOut := extractCLIField(out, "password:")
	mailRedirect(w, r, "added", email+"|"+pwOut)
}

// MailPasswd rotates a mailbox password.
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
	out, err := runSudo(mailboxBinary, "passwd", email)
	if err != nil {
		mailRedirect(w, r, "error", trimOut(err.Error()+"\n"+out))
		return
	}
	pwOut := extractCLIField(out, "new password:")
	mailRedirect(w, r, "passwd", email+"|"+pwOut)
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
		mailRedirect(w, r, "error", trimOut(err.Error()+"\n"+string(out)))
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
	if _, err := runSudo(mailboxBinary, "alias", from, to); err != nil {
		mailRedirect(w, r, "error", trimOut(err.Error()))
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
	if _, err := runSudo(mailboxBinary, "unalias", from); err != nil {
		mailRedirect(w, r, "error", trimOut(err.Error()))
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
	if _, err := runSudo(args...); err != nil {
		mailRedirect(w, r, "error", trimOut(err.Error()))
		return
	}
	display := size
	if display == "" {
		display = "default"
	}
	mailRedirect(w, r, "quota", email+"|"+display)
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
		out = append(out, Mailbox{Email: email, UsedBytes: used, QuotaBytes: quotaBytes})
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
