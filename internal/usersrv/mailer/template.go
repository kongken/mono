package mailer

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"math"
	"strings"
	texttemplate "text/template"
	"time"
)

// appNames are the product names shown in mails, keyed by app ID. An app ID
// outside the table gets a neutral name; the app ID itself is never printed.
var appNames = map[string]string{
	"trip-map": "山海足迹",
	"metro":    "地铁足迹",
}

const defaultAppName = "账号服务"

// AppName returns the display name of an app.
func AppName(appID string) string {
	if n, ok := appNames[appID]; ok {
		return n
	}
	return defaultAppName
}

// TemplateData is everything a template may use. It is deliberately small and
// fixed: a caller cannot put markup, headers or free text into a mail.
type TemplateData struct {
	AppName string
	// Link is the verification link built by Links.Build.
	Link string
	// ExpiresIn is how long the link stays valid, shown in whole minutes.
	ExpiresIn time.Duration
}

// Content is a rendered mail without envelope.
type Content struct {
	Subject string
	Text    string
	HTML    string
}

type view struct {
	AppName string
	Link    string
	Minutes int
}

type definition struct {
	subject string
	text    *texttemplate.Template
	html    *htmltemplate.Template
}

var definitions = map[Kind]definition{
	KindVerifyEmail: {
		subject: "【%s】请验证你的邮箱",
		text: texttemplate.Must(texttemplate.New("verify_text").Parse(`你好，

我们收到了使用这个邮箱注册{{.AppName}}账号的申请。请在 {{.Minutes}} 分钟内打开下面的链接，设置密码并完成注册：

{{.Link}}

如果这不是你本人的操作，请忽略这封邮件，不会创建任何账号。
请不要把这个链接转发给他人。

—— {{.AppName}}
`)),
		html: htmltemplate.Must(htmltemplate.New("verify_html").Parse(htmlLayout(
			`你好，`,
			`我们收到了使用这个邮箱注册{{.AppName}}账号的申请。请在 {{.Minutes}} 分钟内点击下面的按钮，设置密码并完成注册。`,
			`验证邮箱并设置密码`,
			`如果这不是你本人的操作，请忽略这封邮件，不会创建任何账号。请不要把这个链接转发给他人。`))),
	},
	KindResetPassword: {
		subject: "【%s】重置密码",
		text: texttemplate.Must(texttemplate.New("reset_text").Parse(`你好，

我们收到了重置{{.AppName}}账号密码的申请。请在 {{.Minutes}} 分钟内打开下面的链接，设置新密码：

{{.Link}}

如果这不是你本人的操作，请忽略这封邮件，你的密码不会改变。
请不要把这个链接转发给他人。

—— {{.AppName}}
`)),
		html: htmltemplate.Must(htmltemplate.New("reset_html").Parse(htmlLayout(
			`你好，`,
			`我们收到了重置{{.AppName}}账号密码的申请。请在 {{.Minutes}} 分钟内点击下面的按钮，设置新密码。`,
			`设置新密码`,
			`如果这不是你本人的操作，请忽略这封邮件，你的密码不会改变。请不要把这个链接转发给他人。`))),
	},
}

// htmlLayout is the one HTML frame of every mail: no images, no scripts, no
// third-party resources, inline styles only. The link appears twice, as a button
// and as text to copy. html/template escapes every value for its context.
func htmlLayout(greeting, lead, button, footer string) string {
	return `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="referrer" content="no-referrer"></head>
<body style="margin:0;padding:24px;background:#f5f7f4;font-family:-apple-system,'PingFang SC','Microsoft YaHei',sans-serif;color:#1f2a24;">
<div style="max-width:520px;margin:0 auto;background:#ffffff;border-radius:12px;padding:28px;">
<p style="margin:0 0 12px;font-size:16px;">` + greeting + `</p>
<p style="margin:0 0 20px;font-size:15px;line-height:1.7;">` + lead + `</p>
<p style="margin:0 0 20px;"><a href="{{.Link}}" style="display:inline-block;padding:11px 22px;background:#2f7d4f;color:#ffffff;text-decoration:none;border-radius:8px;font-size:15px;">` + button + `</a></p>
<p style="margin:0 0 6px;font-size:13px;color:#5b6a61;">按钮无法点击时，请复制下面的链接到浏览器打开：</p>
<p style="margin:0 0 20px;font-size:13px;word-break:break-all;color:#2f7d4f;">{{.Link}}</p>
<p style="margin:0;font-size:13px;line-height:1.6;color:#5b6a61;">` + footer + `</p>
</div>
</body></html>
`
}

// Render fills the fixed template of kind. It refuses a link that is not a plain
// URL, so a malformed value cannot break out of the attribute or the text.
func Render(kind Kind, d TemplateData) (Content, error) {
	def, ok := definitions[kind]
	if !ok {
		return Content{}, errors.New("mailer: unknown template")
	}
	if d.AppName == "" || hasControl(d.AppName) || strings.ContainsAny(d.AppName, "<>&\"'") {
		return Content{}, errors.New("mailer: invalid app name")
	}
	if !strings.HasPrefix(d.Link, "https://") && !strings.HasPrefix(d.Link, "http://") {
		return Content{}, errors.New("mailer: invalid link")
	}
	if strings.ContainsAny(d.Link, " \t\r\n\"'<>\\`") || hasControl(d.Link) {
		return Content{}, errors.New("mailer: invalid link")
	}
	minutes := int(math.Ceil(d.ExpiresIn.Minutes()))
	if minutes < 1 {
		minutes = 1
	}
	v := view{AppName: d.AppName, Link: d.Link, Minutes: minutes}
	var text, html bytes.Buffer
	if err := def.text.Execute(&text, v); err != nil {
		return Content{}, fmt.Errorf("mailer: render text: %w", err)
	}
	if err := def.html.Execute(&html, v); err != nil {
		return Content{}, fmt.Errorf("mailer: render html: %w", err)
	}
	subject := fmt.Sprintf(def.subject, d.AppName)
	if hasLineBreak(subject) {
		return Content{}, errors.New("mailer: invalid subject")
	}
	return Content{Subject: subject, Text: text.String(), HTML: html.String()}, nil
}
