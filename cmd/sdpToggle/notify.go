package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// notify shows a Windows toast without a console window.
func notify(title, text string) {
	var xml strings.Builder
	xml.WriteString(`<toast><visual><binding template="ToastText02">`)
	xml.WriteString(`<text id="1">` + escapeXML(title) + `</text>`)
	xml.WriteString(`<text id="2">` + escapeXML(text) + `</text>`)
	xml.WriteString(`</binding></visual></toast>`)

	psScript := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml(%q)
$t = New-Object Windows.UI.Notifications.ToastNotification $xml
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("SDP Toggle").Show($t)`,
		xml.String())

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = hideWindow()
	_ = cmd.Run() // best-effort; notification failure is non-fatal
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
