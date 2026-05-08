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

const auditAlertHTML = `<!DOCTYPE html>
<html><body style="font-family: system-ui, -apple-system, sans-serif; padding: 24px; background:#fef7ff; color:#1d1b20; margin:0;">
<div style="max-width:560px; margin:0 auto; background:#fff; border-radius:12px; padding:32px; border-left:4px solid {{.AccentColor}};">
<p style="text-transform:uppercase; letter-spacing:0.08em; font-size:12px; color:{{.AccentColor}}; margin:0 0 8px; font-weight:600;">{{.Headline}}</p>
<h1 style="margin:0 0 16px; font-size:22px;">{{.StoreName}}</h1>

{{if eq .Trigger "audit_failed"}}
  <p style="background:#fceef0; color:#b3261e; padding:12px 16px; border-radius:8px; margin:0 0 16px;">
    <strong>The audit failed to complete.</strong>{{if .Audit.Error}}<br>{{.Audit.Error}}{{end}}
  </p>
{{else}}
  <table style="width:100%; border-collapse:collapse; margin-bottom:16px;">
    <tr>
      <td style="padding:12px 0; border-bottom:1px solid #ECE6F0; vertical-align:top;">
        <div style="color:#49454F; font-size:12px;">Score</div>
        <div style="font-size:28px; font-weight:700; color:#1d1b20;">
          {{.Audit.Score}} <span style="font-size:14px; font-weight:400; color:#49454F;">/ 100</span>
        </div>
      </td>
      {{if .HavePrevScore}}
      <td style="padding:12px 0; border-bottom:1px solid #ECE6F0; vertical-align:top;">
        <div style="color:#49454F; font-size:12px;">Change</div>
        <div style="font-size:20px; font-weight:700; color:{{if lt .ScoreDelta 0}}#b3261e{{else if gt .ScoreDelta 0}}#1b873f{{else}}#49454F{{end}};">
          {{if gt .ScoreDelta 0}}+{{end}}{{.ScoreDelta}}
        </div>
        <div style="color:#49454F; font-size:12px;">prev was {{.PrevScore}}</div>
      </td>
      {{end}}
      <td style="padding:12px 0; border-bottom:1px solid #ECE6F0; vertical-align:top;">
        <div style="color:#49454F; font-size:12px;">Risk</div>
        <div style="font-size:18px; font-weight:600; text-transform:capitalize;">{{.Audit.RiskLevel}}</div>
      </td>
    </tr>
  </table>
  <p style="margin:0 0 16px;"><strong>{{.NewCount}}</strong> new · <strong>{{.NewCriticalCount}}</strong> critical · <strong>{{.ResolvedCount}}</strong> resolved</p>

  {{if .TopNewIssues}}
  <h2 style="margin:24px 0 8px; font-size:16px;">Top new issues</h2>
  <ol style="padding-left:20px; margin:0 0 16px;">
    {{range .TopNewIssues}}
    <li style="margin-bottom:8px;">
      <span style="display:inline-block; padding:2px 8px; border-radius:9999px; font-size:11px; text-transform:uppercase; font-weight:600;
        background:{{if eq .Severity "critical"}}#fceef0{{else if eq .Severity "error"}}#fceef0{{else if eq .Severity "warning"}}#fdf3e1{{else}}#ECE6F0{{end}};
        color:{{if eq .Severity "critical"}}#b3261e{{else if eq .Severity "error"}}#b3261e{{else if eq .Severity "warning"}}#7d5a00{{else}}#49454F{{end}};
        ">{{.Severity}}</span>
      <strong>{{.Title}}</strong>
      {{if .ProductTitle}}<br><span style="color:#49454F; font-size:13px;">on {{.ProductTitle}}</span>{{end}}
      {{if .PageURL}}<br><a href="{{.PageURL}}" style="color:#6750A4; font-size:12px;">{{.PageURL}}</a>{{end}}
    </li>
    {{end}}
  </ol>
  {{end}}
{{end}}

<p style="margin:24px 0 0;">
  <a href="{{.ReportURL}}" style="display:inline-block; padding:12px 24px; background:#6750A4; color:#fff; border-radius:9999px; text-decoration:none;">View full report</a>
</p>

<p style="margin-top:32px; padding-top:16px; border-top:1px solid #ECE6F0; color:#79747E; font-size:12px;">
  You're receiving this because you subscribed to <code>{{.Trigger}}</code> alerts for <strong>{{.StoreName}}</strong>.<br>
  <a href="{{.UnsubscribeURL}}" style="color:#79747E;">Unsubscribe from {{.Trigger}}</a> · gmcauditor
</p>
</div>
</body></html>`

const gmcRevokedHTML = `<!DOCTYPE html>
<html><body style="font-family: system-ui, -apple-system, sans-serif; padding: 24px; background:#fef7ff; color:#1d1b20;">
<div style="max-width:520px; margin:0 auto; background:#fff; border-radius:12px; padding:32px; border-left:4px solid #b3261e;">
<p style="text-transform:uppercase; letter-spacing:0.08em; font-size:12px; color:#b3261e; margin:0 0 8px; font-weight:600;">Action required</p>
<h1 style="margin:0 0 12px;">Google Merchant Center disconnected</h1>
<p>{{ if .Name }}Hi {{ .Name }},{{ end }}</p>
<p>Google rejected our refresh token for store <strong>{{ .StoreName }}</strong> (Merchant ID <code>{{ .MerchantID }}</code>). This typically happens when the linked Google account revoked access or our app credentials changed.</p>
<p>Future audits will run with crawler data only until you re-authorize.</p>
<p style="margin:24px 0;">
  <a href="{{ .ReconnectURL }}" style="display:inline-block; padding:12px 24px; background:#6750A4; color:#fff; border-radius:9999px; text-decoration:none;">Reconnect Google Merchant Center</a>
</p>
<p style="color:#49454F; font-size:13px;">If you intended to disconnect, you can ignore this email.</p>
</div>
</body></html>`

var (
	tplVerify        = template.Must(template.New("verify").Parse(verifyEmailHTML))
	tplInvitation    = template.Must(template.New("invite").Parse(invitationHTML))
	tplPasswordReset = template.Must(template.New("reset").Parse(passwordResetHTML))
	tplImpersonation = template.Must(template.New("impersonation").Parse(impersonationHTML))
	tplAuditAlert    = template.Must(template.New("audit-alert").Parse(auditAlertHTML))
	tplGMCRevoked    = template.Must(template.New("gmc-revoked").Parse(gmcRevokedHTML))
)

type GMCRevokedData struct {
	Name         string
	StoreName    string
	MerchantID   string
	ReconnectURL string
}

func RenderGMCRevoked(d GMCRevokedData) (string, error) { return render(tplGMCRevoked, d) }

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

type AuditAlertAudit struct {
	Status    string
	Score     int
	RiskLevel string
	Error     string
}

type AuditAlertIssue struct {
	Severity     string
	Title        string
	ProductTitle string
	PageURL      string
}

type AuditAlertData struct {
	Trigger          string
	StoreName        string
	ReportURL        string
	UnsubscribeURL   string
	Audit            AuditAlertAudit
	NewCount         int
	ResolvedCount    int
	NewCriticalCount int
	PrevScore        int
	HavePrevScore    bool
	ScoreDelta       int
	TopNewIssues     []AuditAlertIssue
}

func (d AuditAlertData) Headline() string {
	switch d.Trigger {
	case "new_critical":
		return "New critical issue"
	case "score_drop":
		return "Score dropped"
	case "audit_failed":
		return "Audit failed"
	case "gmc_account_change":
		return "GMC account change"
	}
	return "Audit update"
}

func (d AuditAlertData) AccentColor() string {
	switch d.Trigger {
	case "audit_failed", "new_critical":
		return "#b3261e"
	case "score_drop":
		return "#b76b00"
	}
	return "#6750A4"
}

func RenderAuditAlert(d AuditAlertData) (string, error) { return render(tplAuditAlert, d) }

func render(t *template.Template, data any) (string, error) {
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("mailer: render: %w", err)
	}
	return b.String(), nil
}
