package mailer

import (
	"bytes"
	"fmt"
	"html/template"
)

const verifyEmailHTML = `<!DOCTYPE html>
<html><body style="font-family: system-ui, -apple-system, sans-serif; padding: 24px; background:#fef7ff; color:#1d1b20;">
<div style="max-width:520px; margin:0 auto; background:#fff; border-radius:12px; padding:32px;">
<h1 style="margin-top:0;">Confirm your email</h1>
<p>Welcome to gmcauditor, {{.Name}}.</p>
<p>Click the link below to confirm your email address. This link expires in 24 hours.</p>
<p><a href="{{.URL}}" style="display:inline-block; padding:12px 24px; background:#6750A4; color:#fff; border-radius:9999px; text-decoration:none;">Confirm email</a></p>
<p style="color:#49454F; font-size:14px;">Or paste this URL into your browser:<br><code>{{.URL}}</code></p>
</div>
</body></html>`

const invitationHTML = `<!DOCTYPE html>
<html><body style="font-family: system-ui, -apple-system, sans-serif; padding: 24px; background:#fef7ff; color:#1d1b20;">
<div style="max-width:520px; margin:0 auto; background:#fff; border-radius:12px; padding:32px;">
<h1 style="margin-top:0;">You're invited to {{.Tenant}}</h1>
<p>{{.Inviter}} invited you to join the workspace as {{.Role}}.</p>
<p><a href="{{.URL}}" style="display:inline-block; padding:12px 24px; background:#6750A4; color:#fff; border-radius:9999px; text-decoration:none;">Accept invitation</a></p>
<p style="color:#49454F; font-size:14px;">This invitation expires in 7 days.</p>
</div>
</body></html>`

const passwordResetHTML = `<!DOCTYPE html>
<html><body style="font-family: system-ui, -apple-system, sans-serif; padding: 24px; background:#fef7ff; color:#1d1b20;">
<div style="max-width:520px; margin:0 auto; background:#fff; border-radius:12px; padding:32px;">
<h1 style="margin-top:0;">Reset your password</h1>
<p>If you requested a password reset, click below. This link expires in 1 hour.</p>
<p><a href="{{.URL}}" style="display:inline-block; padding:12px 24px; background:#6750A4; color:#fff; border-radius:9999px; text-decoration:none;">Reset password</a></p>
<p style="color:#49454F; font-size:14px;">If you didn't request a reset, you can ignore this email.</p>
</div>
</body></html>`

const impersonationHTML = `<!DOCTYPE html>
<html><body style="font-family: system-ui, -apple-system, sans-serif; padding: 24px; background:#fef7ff; color:#1d1b20;">
<div style="max-width:520px; margin:0 auto; background:#fff; border-radius:12px; padding:32px; border-left:4px solid #B3261E;">
<h1 style="margin-top:0; color:#B3261E;">Account access notice</h1>
<p>A platform admin ({{.Admin}}) has accessed {{.Tenant}} on {{.At}}.</p>
<p>Reason: <em>{{.Reason}}</em></p>
<p style="color:#49454F; font-size:14px;">If this looks unexpected, contact support immediately.</p>
</div>
</body></html>`

var (
	tplVerify        = template.Must(template.New("verify").Parse(verifyEmailHTML))
	tplInvitation    = template.Must(template.New("invite").Parse(invitationHTML))
	tplPasswordReset = template.Must(template.New("reset").Parse(passwordResetHTML))
	tplImpersonation = template.Must(template.New("impersonation").Parse(impersonationHTML))
)

type VerifyEmailData struct {
	Name string
	URL  string
}

func RenderVerifyEmail(d VerifyEmailData) (string, error) { return render(tplVerify, d) }

type InvitationData struct {
	Tenant  string
	Inviter string
	Role    string
	URL     string
}

func RenderInvitation(d InvitationData) (string, error) { return render(tplInvitation, d) }

type PasswordResetData struct {
	URL string
}

func RenderPasswordReset(d PasswordResetData) (string, error) { return render(tplPasswordReset, d) }

type ImpersonationData struct {
	Admin  string
	Tenant string
	At     string
	Reason string
}

func RenderImpersonation(d ImpersonationData) (string, error) { return render(tplImpersonation, d) }

func render(t *template.Template, data any) (string, error) {
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("mailer: render: %w", err)
	}
	return b.String(), nil
}
