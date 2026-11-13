package community

import (
	"net/http"
	"net/url"
	"testing"
)

const cookiePathInsecure = "http://steamcommunity.com/"

func TestSetCookies(t *testing.T) {
	client := http.Client{}
	SetCookies(&client, "x", "y")

	path, err := url.Parse(cookiePathInsecure)
	if err != nil {
		t.Fatal(err)
	}

	// Test to make sure that insecure (http) isn't in the jar, but that secure (https) is
	if got := client.Jar.Cookies(path); len(got) != 0 {
		t.Fatalf("got %d cookies for insecure path, want 0", len(got))
	}

	path, err = url.Parse(cookiePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.Jar.Cookies(path); len(got) != 2 {
		t.Fatalf("got %d cookies for secure path, want 2", len(got))
	}
}
