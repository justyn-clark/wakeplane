package main

import (
	"fmt"
	"html"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize notification copy without changing the source report in the ledger.
func asciiText(text string) string {
	text = strings.NewReplacer("\u2018", "'", "\u2019", "'", "\u201c", "\"", "\u201d", "\"",
		"\u2013", "-", "\u2014", "-", "\u2011", "-", "\u2026", "...", "\u00df", "ss",
		"\u00c6", "AE", "\u00e6", "ae", "\u0152", "OE", "\u0153", "oe", "\u00d8", "O", "\u00f8", "o",
		"\u0141", "L", "\u0142", "l").Replace(text)
	var out strings.Builder
	for _, char := range norm.NFKD.String(text) {
		switch {
		case char == '\n' || char == '\t' || char >= 32 && char <= 126:
			out.WriteRune(char)
		case unicode.IsSpace(char):
			out.WriteByte(' ')
		}
	}
	return strings.TrimSpace(out.String())
}

// Escape non-ASCII URL bytes instead of transliterating a link's destination.
func asciiURL(address string) string {
	u, err := validHTTPURL(address)
	if err != nil {
		return ""
	}
	var out strings.Builder
	for _, char := range []byte(u.String()) {
		if char >= 128 {
			fmt.Fprintf(&out, "%%%02X", char)
		} else {
			out.WriteByte(char)
		}
	}
	return out.String()
}

func reportDate(value string) string {
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at.UTC().Format("Jan 2, 2006 at 15:04 UTC")
	}
	return asciiText(value)
}

func wrapEmailText(text string) string {
	var lines []string
	var line string
	for _, word := range strings.Fields(asciiText(text)) {
		if len(line)+1+len(word) > 68 && line != "" {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	return strings.Join(append(lines, line), "\n")
}

func renderEmailText(result report) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n%s\n\n%s\n", asciiText(result.Title), reportDate(result.GeneratedAt.Format(time.RFC3339)), wrapEmailText(result.Summary))
	for i, item := range result.Items {
		fmt.Fprintf(&out, "\n\n%d. %s\n\n", i+1, asciiText(item.Title))
		if item.Source != "" {
			fmt.Fprintf(&out, "Source: %s\n", asciiText(item.Source))
		}
		if item.PublishedAt != "" {
			fmt.Fprintf(&out, "Published: %s\n", reportDate(item.PublishedAt))
		}
		if item.Excerpt != "" {
			fmt.Fprintf(&out, "\n%s\n", wrapEmailText(item.Excerpt))
		}
		if link := asciiURL(item.URL); link != "" {
			fmt.Fprintf(&out, "\n%s\n", link)
		}
	}
	return out.String()
}

func renderEmailHTML(result report) string {
	escape := func(text string) string { return html.EscapeString(asciiText(text)) }
	var out strings.Builder
	out.WriteString(`<!doctype html><html lang="en"><head><meta charset="us-ascii"><meta name="viewport" content="width=device-width, initial-scale=1"><title>`)
	out.WriteString(escape(result.Title))
	out.WriteString(`</title></head><body style="margin:0;padding:0;background:#f4f5f7;color:#20242a;"><table role="presentation" style="width:100%;border-collapse:collapse;"><tr><td align="center" style="padding:24px 12px;"><table role="presentation" style="width:100%;max-width:600px;border-collapse:collapse;background:#ffffff;"><tr><td style="padding:28px 24px;font-family:Arial,Helvetica,sans-serif;font-size:16px;line-height:1.6;overflow-wrap:anywhere;word-break:break-word;">`)
	fmt.Fprintf(&out, `<h1 style="margin:0 0 8px;font-size:24px;line-height:1.3;font-weight:600;">%s</h1><p style="margin:0 0 24px;color:#626a75;font-size:13px;">%s</p><p style="margin:0 0 8px;">%s</p>`, escape(result.Title), escape(reportDate(result.GeneratedAt.Format(time.RFC3339))), escape(result.Summary))
	for _, item := range result.Items {
		out.WriteString(`<hr style="margin:28px 0;border:0;border-top:1px solid #e4e7eb;">`)
		fmt.Fprintf(&out, `<h2 style="margin:0 0 8px;font-size:19px;line-height:1.4;font-weight:600;">%s</h2>`, escape(item.Title))
		metadata := escape(item.Source)
		if item.PublishedAt != "" {
			if metadata != "" {
				metadata += "<br>"
			}
			metadata += escape(reportDate(item.PublishedAt))
		}
		if metadata != "" {
			fmt.Fprintf(&out, `<p style="margin:0 0 16px;color:#626a75;font-size:13px;line-height:1.5;">%s</p>`, metadata)
		}
		if item.Excerpt != "" {
			fmt.Fprintf(&out, `<p style="margin:0 0 16px;">%s</p>`, escape(item.Excerpt))
		}
		if link := asciiURL(item.URL); link != "" {
			fmt.Fprintf(&out, `<p style="margin:0;"><a href="%s" style="color:#155bbb;text-decoration:underline;">Read more</a></p>`, html.EscapeString(link))
		}
	}
	out.WriteString(`</td></tr></table></td></tr></table></body></html>`)
	return out.String()
}
