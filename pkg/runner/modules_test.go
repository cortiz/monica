package runner_test

import (
	"bytes"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/runner"
)

func TestRunRequire(t *testing.T) {
	srv, _ := apiServer(t)
	tests := []struct {
		name    string
		files   map[string]string // relative to the project dir
		mon     string            // path of the .mon file to run
		wantErr string
	}{
		{
			name: "project file and scripts.paths from a nested .mon",
			files: map[string]string{
				"monica.yml":      "version: 1\nscripts:\n  paths: [./lib]\n",
				"lib/auth.js":     "exports.bearer = (t) => 'Bearer ' + t;\n",
				"helpers/x.js":    "module.exports = 41;\n",
				"api/users/a.mon": "< {%\n  const { bearer } = require('auth');\n  req.headers.set('X-Pre', bearer(require('../../helpers/x') + 1));\n%}\nGET " + srv.URL + "/echo\n",
			},
			mon: "api/users/a.mon",
		},
		{
			name: "TypeScript lib, import in a block and a .ts script file",
			files: map[string]string{
				"monica.yml":      "version: 1\nscripts:\n  paths: [./lib]\n",
				"lib/auth.ts":     "export const bearer = (n: number): string => `Bearer ${n}`;\n",
				"tests/check.ts":  "import { bearer } from 'auth';\nconst want: string = bearer(42);\ntest('ts', () => expect(res.body).toContain(want));\n",
				"api/users/a.mon": "< {%\n  import { bearer } from 'auth';\n  req.headers.set('X-Pre', bearer(42));\n%}\nGET " + srv.URL + "/echo\n\n> ../../tests/check.ts\n",
			},
			mon: "api/users/a.mon",
		},
		{
			name: "traversal above the config folder is rejected",
			files: map[string]string{
				"monica.yml": "version: 1\n",
				"a.mon":      "< {% require('../outside.js') %}\nGET " + srv.URL + "/never\n",
			},
			mon:     "a.mon",
			wantErr: "is outside the project root and scripts.paths",
		},
		{
			name: "without monica.yml the root is the .mon file's folder",
			files: map[string]string{
				"sub/a.mon": "< {% require('../up.js') %}\nGET " + srv.URL + "/never\n",
				"up.js":     "module.exports = 1;\n",
			},
			mon:     "sub/a.mon",
			wantErr: "is outside the project root",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				write(t, dir, name, content)
			}
			var out bytes.Buffer
			err := runner.RunSingleFile(dir+"/"+tt.mon, &runner.Opts{}, &out)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), "x-pre=Bearer 42") {
					t.Fatalf("modules not applied: %q", out.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
		})
	}
}
