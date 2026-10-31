package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPathLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"a.mon", "b.mon", true},
		{"b.mon", "a.mon", false},
		{"a/z.mon", "a.mon", true}, // folder "a" sorts before file "a.mon"
		{"a.mon", "b/a.mon", true},
		{"a/b/c.mon", "a/b.mon", true}, // same at every level
		{"x/a.mon", "x/a.mon", false},
		{"./a.mon", "a.mon", false},
	}
	for _, tt := range tests {
		if got := pathLess(tt.a, tt.b); got != tt.want {
			t.Errorf("pathLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestOrder(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string // extra files (configs) by relative path
		input  []string
		want   []string
		config string // --config, relative to the tree
	}{
		{
			name:  "no config: path order",
			input: []string{"b.mon", "a/z.mon", "a.mon", "a/b/c.mon"},
			want:  []string{"a/b/c.mon", "a/z.mon", "a.mon", "b.mon"},
		},
		{
			name:  "config without order: path order",
			files: map[string]string{"monica.yml": "version: 1\n"},
			input: []string{"b.mon", "a.mon"},
			want:  []string{"a.mon", "b.mon"},
		},
		{
			name:  "order entries first, in list order, rest by path",
			files: map[string]string{"monica.yml": "version: 1\norder: [users, auth/login.mon]\n"},
			input: []string{"a.mon", "auth/logout.mon", "auth/login.mon", "users/b.mon", "users/a.mon", "z.mon"},
			want:  []string{"users/a.mon", "users/b.mon", "auth/login.mon", "a.mon", "auth/logout.mon", "z.mon"},
		},
		{
			name:  "first matching entry wins",
			files: map[string]string{"monica.yml": "version: 1\norder: [auth/, ./auth/login.mon, b.mon]\n"},
			input: []string{"b.mon", "auth/x.mon", "auth/login.mon"},
			want:  []string{"auth/login.mon", "auth/x.mon", "b.mon"},
		},
		{
			name: "each config orders its own files, in their slots",
			files: map[string]string{
				"monica.yml":     "version: 1\norder: [z.mon]\n",
				"sub/monica.yml": "version: 1\norder: [y.mon]\n",
			},
			input: []string{"a.mon", "sub/x.mon", "sub/y.mon", "z.mon"},
			want:  []string{"z.mon", "sub/y.mon", "sub/x.mon", "a.mon"},
		},
		{
			name:   "--config order applies to every file",
			files:  map[string]string{"cfg/monica.yml": "version: 1\norder: [b.mon]\n"},
			config: "cfg/monica.yml",
			input:  []string{"a.mon", "cfg/b.mon"},
			want:   []string{"cfg/b.mon", "a.mon"},
		},
		{
			name:  "invalid config: path order",
			files: map[string]string{"monica.yml": "version: 1\norder: [b.mon]\nnope: 1\n"},
			input: []string{"b.mon", "a.mon"},
			want:  []string{"a.mon", "b.mon"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(rel, content string) {
				p := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for rel, content := range tt.files {
				write(rel, content)
			}
			abs := func(rels []string) []string {
				out := make([]string, len(rels))
				for i, rel := range rels {
					out[i] = filepath.Join(root, rel)
				}
				return out
			}
			for _, f := range abs(tt.input) {
				write(f[len(root)+1:], "GET http://a.example/\n")
			}
			opts := &Opts{}
			if tt.config != "" {
				opts.ConfigPath = filepath.Join(root, tt.config)
			}
			got := New(opts, &discard{}).Order(abs(tt.input))
			if want := abs(tt.want); !reflect.DeepEqual(got, want) {
				t.Fatalf("Order:\n got %v\nwant %v", got, want)
			}
		})
	}
}

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }
