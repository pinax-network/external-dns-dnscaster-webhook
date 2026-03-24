package webhook

import "testing"

func TestMediaTypeVersionBuilderAndMatch(t *testing.T) {
	t.Parallel()

	v1 := mediaTypeVersion("1")
	if !v1.Is("application/external.dns.webhook+json;version=1") {
		t.Fatalf("expected versioned media type to match")
	}
	if v1.Is("application/json") {
		t.Fatalf("did not expect generic media type to match")
	}
}

func TestCheckAndGetMediaTypeHeaderValue(t *testing.T) {
	t.Parallel()

	version, err := checkAndGetMediaTypeHeaderValue("application/external.dns.webhook+json;version=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "1" {
		t.Fatalf("expected version 1, got %s", version)
	}
}

func TestCheckAndGetMediaTypeHeaderValueRejectsInvalidValue(t *testing.T) {
	t.Parallel()

	_, err := checkAndGetMediaTypeHeaderValue("application/external.dns.webhook+json;version=2")
	if err == nil {
		t.Fatalf("expected error")
	}
}
