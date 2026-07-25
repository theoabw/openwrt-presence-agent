package identity

import "testing"

func TestClientID(t *testing.T) {
	t.Parallel()
	got, err := ClientID("AA-BB-CC-DD-EE-FF")
	if err != nil {
		t.Fatal(err)
	}
	if got != "mac:aa:bb:cc:dd:ee:ff" {
		t.Fatalf("ClientID() = %q", got)
	}
	if _, err := ClientID("not-a-mac"); err == nil {
		t.Fatal("ClientID() accepted invalid input")
	}
}
