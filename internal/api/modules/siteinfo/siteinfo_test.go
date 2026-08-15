package siteinfo

import (
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestParseInfo(t *testing.T) {
	base := mustURL(t, "https://example.com/blog/post.html")

	tests := []struct {
		name      string
		html      string
		wantTitle string
		wantDesc  string
		wantIcon  string
	}{
		{
			name:      "title trimmed and shortcut icon resolved against page dir",
			html:      `<html><head><title> Hello World </title><meta property="og:description" content="og desc"><link rel="shortcut icon" href="favicon.ico"></head></html>`,
			wantTitle: "Hello World",
			wantDesc:  "og desc",
			wantIcon:  "https://example.com/blog/favicon.ico",
		},
		{
			name:      "og title description and image fallbacks",
			html:      `<head><meta property="og:title" content="OG Title"><meta name="description" content="meta desc"><meta property="og:image" content="https://cdn.example.com/cover.png"></head>`,
			wantTitle: "OG Title",
			wantDesc:  "meta desc",
			wantIcon:  "https://cdn.example.com/cover.png",
		},
		{
			name:     "og description wins over name description",
			html:     `<head><meta name="description" content="meta desc"><meta property="og:description" content="og desc"></head>`,
			wantDesc: "og desc",
		},
		{
			name:     "apple touch icon wins over icon regardless of order",
			html:     `<head><link rel="icon" href="/i.png"><link rel="apple-touch-icon" href="/apple.png"></head>`,
			wantIcon: "https://example.com/apple.png",
		},
		{
			name:     "twitter image fallback",
			html:     `<head><meta property="twitter:image" content="/tw.png"></head>`,
			wantIcon: "https://example.com/tw.png",
		},
		{
			name:     "data image icon falls back to icon-like link",
			html:     `<head><link rel="icon" href="data:image/png;base64,xx"><link rel="mask-icon" href="/mask.svg"></head>`,
			wantIcon: "https://example.com/mask.svg",
		},
		{
			name:     "parent relative icon resolved",
			html:     `<head><link rel="icon" href="../favicon.ico"></head>`,
			wantIcon: "https://example.com/favicon.ico",
		},
		{
			name:     "javascript icon dropped",
			html:     `<head><link rel="icon" href="javascript:alert(1)"></head>`,
			wantIcon: "",
		},
		{
			name:     "protocol relative icon resolved with base scheme",
			html:     `<head><link rel="icon" href="//cdn.example.com/f.png"></head>`,
			wantIcon: "https://cdn.example.com/f.png",
		},
		{
			name:      "empty title element does not fall back to og:title",
			html:      `<head><title></title><meta property="og:title" content="OG"></head>`,
			wantTitle: "",
		},
		{
			name: "no matches",
			html: `<html><body><p>hello</p></body></html>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, desc, icon := parseInfo(tt.html, base)
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			if desc != tt.wantDesc {
				t.Errorf("desc = %q, want %q", desc, tt.wantDesc)
			}
			if icon != tt.wantIcon {
				t.Errorf("icon = %q, want %q", icon, tt.wantIcon)
			}
		})
	}
}

func TestResolveIcon(t *testing.T) {
	base := mustURL(t, "https://example.com")

	tests := []struct {
		name string
		icon string
		want string
	}{
		{"absolute http", "http://other.com/i.png", "http://other.com/i.png"},
		{"root relative", "/i.png", "https://example.com/i.png"},
		{"protocol relative", "//cdn.example.com/i.png", "https://cdn.example.com/i.png"},
		{"ftp scheme dropped", "ftp://example.com/i.png", ""},
		{"garbage dropped", "::not a url", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveIcon(base, tt.icon); got != tt.want {
				t.Errorf("resolveIcon(%q) = %q, want %q", tt.icon, got, tt.want)
			}
		})
	}
}
