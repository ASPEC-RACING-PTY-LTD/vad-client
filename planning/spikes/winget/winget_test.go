package main

import (
	"slices"
	"testing"
)

func TestInstallArgs(t *testing.T) {
	tests := []struct {
		name    string
		it      Item
		want    []string // each must appear in the argv
		wantErr bool
	}{
		{"machine pinned", Item{Source: "winget", ID: "Git.Git", Version: "2.47.1", Scope: ScopeMachine, LogPath: `C:\l\git.log`},
			[]string{"--id", "Git.Git", "--version", "2.47.1", "--scope", "machine", "--log"}, false},
		{"store", Item{Source: "msstore", ID: "9NBLGGH4NNS1"}, []string{"--source", "msstore"}, false},
		{"bad source", Item{Source: "choco", ID: "git"}, nil, true},
		{"injection id", Item{Source: "winget", ID: "Git.Git --override /S"}, nil, true},
		{"bad scope", Item{Source: "winget", ID: "Git.Git", Scope: "system"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InstallArgs(tt.it)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			for _, w := range tt.want {
				if !slices.Contains(got, w) {
					t.Errorf("args %v missing %q", got, w)
				}
			}
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		code uint32
		want Outcome
	}{
		{0, Installed},
		{0x8A15010D, AlreadyPresent},
		{0x8A150109, RebootPending},
		{3010, RebootPending},
		{0x8A15010C, UserCancelled},
		{0x8A150014, NotFound},
		{0x8A150107, Retryable},
		{0x8A15FFFF, Failed},
		{1, Failed},
	}
	for _, tt := range tests {
		if got := Classify(tt.code); got != tt.want {
			t.Errorf("Classify(%#x) = %s, want %s", tt.code, got, tt.want)
		}
	}
}
