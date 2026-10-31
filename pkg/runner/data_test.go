package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadDataFixtures(t *testing.T) {
	tests := []struct {
		file string
		want []Row
	}{
		{"testdata/data/users.csv", []Row{
			{"user": "ada", "pass": "s3cret, with comma"},
			{"user": "bob", "pass": ""},
		}},
		{"testdata/data/users.json", []Row{
			{"user": "ada", "id": "7", "admin": "true", "tags": `["a","b"]`, "note": ""},
			{"user": "bob", "id": "1.5e3", "admin": "false", "tags": `{"x":1}`, "note": "hi"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := LoadData(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestLoadDataErrors(t *testing.T) {
	tests := []struct {
		name, file, content, wantErr string
	}{
		{"unknown extension", "d.txt", "a\n1\n", `unsupported data file type ".txt"`},
		{"empty csv", "d.csv", "", "empty file"},
		{"csv header only", "d.csv", "a,b\n", "no data rows"},
		{"csv bad name", "d.csv", "ok,1st\nx,y\n", `line 1, column 2: invalid variable name "1st"`},
		{"csv empty name", "d.csv", "a,\nx,y\n", `invalid variable name ""`},
		{"csv duplicate name", "d.csv", "a,a\nx,y\n", `duplicate variable "a"`},
		{"csv ragged row", "d.csv", "a,b\nx\n", "wrong number of fields"},
		{"json not array", "d.json", `{"a": 1}`, "expected an array of objects"},
		{"json array of scalars", "d.json", `[1]`, "expected an array of objects"},
		{"json null row", "d.json", `[null]`, "row 1: expected an object"},
		{"json bad name", "d.json", `[{"a": 1}, {"b c": 2}]`, `row 2: invalid variable name "b c"`},
		{"json empty array", "d.json", `[]`, "no data rows"},
		{"json trailing data", "d.json", `[{"a": 1}] [{"a": 2}]`, "unexpected data after the array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.file)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadData(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error %q does not name the file", err)
			}
		})
	}
	if _, err := LoadData(filepath.Join(t.TempDir(), "missing.csv")); err == nil || !strings.Contains(err.Error(), "reading data file") {
		t.Fatalf("missing file: %v", err)
	}
}
