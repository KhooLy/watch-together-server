package main

import "testing"

func TestOriginList(t *testing.T) {
	origins := map[string]struct{}{"https://example.com": {}}
	if _, ok := origins["https://example.com"]; !ok {
		t.Fatal("expected configured origin")
	}
	if _, ok := origins["https://other.example"]; ok {
		t.Fatal("unexpected unconfigured origin")
	}
}

func TestMessageValidationRejectsOversizedFields(t *testing.T) {
	message := clientMessage{Name: string(make([]rune, 129))}
	if validateMessage(message) == nil {
		t.Fatal("expected oversized name to be rejected")
	}
}
