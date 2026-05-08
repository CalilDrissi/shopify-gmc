package web

import (
	"bufio"
	"errors"
	"fmt"
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
)

var emailRE = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)

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

// readMailboxes parses /etc/dovecot/users (one mailbox per line, fields
// colon-separated). The page only needs the email, not the hash.
func readMailboxes() ([]string, error) {
	f, err := os.Open(dovecotUsers)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
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
		out = append(out, line[:i])
	}
	sort.Strings(out)
	return out, s.Err()
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
