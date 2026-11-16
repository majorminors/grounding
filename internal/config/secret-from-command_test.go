package config

import (
	"strings"
	"testing"
)

func TestSecretFromCommandReturnsTheFirstLineTrimmed(t *testing.T) {
	secret, err := SecretFromCommand(`printf '  the-key  \nnotes: not the key\n'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "the-key" {
		t.Errorf("secret = %q, want %q", secret, "the-key")
	}
}

func TestSecretFromCommandRefusesACommandThatFails(t *testing.T) {
	_, err := SecretFromCommand("exit 3")
	if err == nil || !strings.Contains(err.Error(), "exit 3") {
		t.Errorf("want an error naming the command, got %v", err)
	}
}

func TestSecretFromCommandRefusesACommandThatPrintsNothing(t *testing.T) {
	_, err := SecretFromCommand("true")
	if err == nil || !strings.Contains(err.Error(), "printed nothing") {
		t.Errorf("want a printed-nothing error, got %v", err)
	}
}
