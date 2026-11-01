package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/pkg/request"
)

func TestInitFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	results, err := Init(dir, Options{BaseURL: "http://localhost:8080/"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{filepath.Join(dir, "monica.yml"), Created},
		{filepath.Join(dir, "example.mon"), Created},
		{filepath.Join(dir, ".gitignore"), Created},
	}
	if len(results) != len(want) {
		t.Fatalf("results = %v, want %v", results, want)
	}
	for i := range want {
		if results[i] != want[i] {
			t.Errorf("results[%d] = %v, want %v", i, results[i], want[i])
		}
	}

	cfg, err := config.Load(filepath.Join(dir, "monica.yml"))
	if err != nil {
		t.Fatalf("generated monica.yml does not load: %v", err)
	}
	if got := cfg.Defaults.Vars["baseUrl"]; got != "http://localhost:8080" {
		t.Errorf("defaults baseUrl = %q", got)
	}
	if got := cfg.Envs["dev"].Vars["baseUrl"]; got != "http://localhost:8080" {
		t.Errorf("dev baseUrl = %q", got)
	}

	reqs, err := request.ParseMonFile(filepath.Join(dir, "example.mon"))
	if err != nil {
		t.Fatalf("generated example.mon does not parse: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("example.mon has %d requests, want 2", len(reqs))
	}

	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range GitignoreEntries {
		if !strings.Contains(string(gi), "\n"+e+"\n") {
			t.Errorf(".gitignore lacks %q:\n%s", e, gi)
		}
	}
}

func TestInitExisting(t *testing.T) {
	tests := []struct {
		name      string
		force     bool
		gitignore string // existing content; "-" for none
		wantMon   Action
		wantGit   Action
		wantGI    string
	}{
		{"keeps files", false, "-", Skipped, Created, "# Monica: personal and secret values\n.env\nmonica.local.yml\n"},
		{"force overwrites", true, "-", Overwritten, Created, "# Monica: personal and secret values\n.env\nmonica.local.yml\n"},
		{"appends missing entry", false, "dist/\n/.env", Skipped, Updated, "dist/\n/.env\n\n# Monica: personal and secret values\nmonica.local.yml\n"},
		{"complete gitignore untouched", false, "monica.local.yml\n.env\n", Skipped, Skipped, "monica.local.yml\n.env\n"},
		{"empty gitignore", false, "", Skipped, Updated, "# Monica: personal and secret values\n.env\nmonica.local.yml\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "monica.yml"), "mine")
			mustWrite(t, filepath.Join(dir, "example.mon"), "mine")
			if tt.gitignore != "-" {
				mustWrite(t, filepath.Join(dir, ".gitignore"), tt.gitignore)
			}
			results, err := Init(dir, Options{Force: tt.force})
			if err != nil {
				t.Fatal(err)
			}
			if results[0].Action != tt.wantMon || results[1].Action != tt.wantMon {
				t.Errorf("monica.yml/example.mon = %s/%s, want %s", results[0].Action, results[1].Action, tt.wantMon)
			}
			if results[2].Action != tt.wantGit {
				t.Errorf(".gitignore = %s, want %s", results[2].Action, tt.wantGit)
			}
			yml, _ := os.ReadFile(filepath.Join(dir, "monica.yml"))
			if kept := string(yml) == "mine"; kept == tt.force {
				t.Errorf("monica.yml kept = %v with force = %v", kept, tt.force)
			}
			gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
			if string(gi) != tt.wantGI {
				t.Errorf(".gitignore = %q, want %q", gi, tt.wantGI)
			}
		})
	}
}

func TestInitInvalidBaseURL(t *testing.T) {
	for _, u := range []string{"localhost:8080", "ftp://x", "http://", "http://a b", "http://a\nb"} {
		t.Run(u, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Init(dir, Options{BaseURL: u}); err == nil {
				t.Fatal("expected an error")
			}
			if _, err := os.Stat(filepath.Join(dir, "monica.yml")); err == nil {
				t.Error("monica.yml written despite the error")
			}
		})
	}
}

func TestInitQuotesBaseURL(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, Options{BaseURL: `https://h/x#"y`}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, "monica.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Defaults.Vars["baseUrl"]; got != `https://h/x#"y` {
		t.Errorf("baseUrl = %q", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInitRefusesSymlinks(t *testing.T) {
	for _, name := range []string{"monica.yml", "example.mon", ".gitignore"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(t.TempDir(), "outside")
			mustWrite(t, target, "keep")
			if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
				t.Skip("symlinks unavailable:", err)
			}
			if _, err := Init(dir, Options{Force: true}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("err = %v, want a symbolic link error", err)
			}
			if b, _ := os.ReadFile(target); string(b) != "keep" {
				t.Errorf("symlink target changed to %q", b)
			}
		})
	}
}
