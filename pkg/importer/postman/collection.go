// Package postman converts Postman collections (v2.0 / v2.1 JSON) and
// environments into a monica project (`monica import postman`).
package postman

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Collection is a Postman collection file.
type Collection struct {
	Info struct {
		Name   string `json:"name"`
		Schema string `json:"schema"`
	} `json:"info"`
	Item     []Item     `json:"item"`
	Variable []Variable `json:"variable"`
	Auth     *Auth      `json:"auth"`
	Event    []Event    `json:"event"`
}

// Item is a folder (Item set) or a request (Request set).
type Item struct {
	Name    string   `json:"name"`
	Item    []Item   `json:"item"`
	Request *Request `json:"request"`
	Event   []Event  `json:"event"`
	Auth    *Auth    `json:"auth"`
}

// IsFolder reports whether the item groups other items.
func (i Item) IsFolder() bool { return i.Request == nil }

// Request is a Postman request.
type Request struct {
	Method string `json:"method"`
	Header []KV   `json:"header"`
	URL    URL    `json:"url"`
	Body   *Body  `json:"body"`
	Auth   *Auth  `json:"auth"`
}

// KV is a key/value entry (headers, query, form fields, auth parameters).
type KV struct {
	Key      string    `json:"key"`
	Value    flexValue `json:"value"`
	Disabled bool      `json:"disabled"`
	Type     string    `json:"type"`
	Src      flexSrc   `json:"src"`
}

// URL is a request URL: a string, or an object with its parts.
type URL struct {
	Raw      string
	Protocol string
	Host     []string
	Path     []string
	Query    []KV
	Variable []KV
	object   bool
}

// UnmarshalJSON accepts a string or a URL object.
func (u *URL) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		u.Raw = s
		return nil
	}
	var o struct {
		Raw      string          `json:"raw"`
		Protocol string          `json:"protocol"`
		Host     json.RawMessage `json:"host"`
		Path     json.RawMessage `json:"path"`
		Query    []KV            `json:"query"`
		Variable []KV            `json:"variable"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("url: %w", err)
	}
	u.object = true
	u.Raw, u.Protocol, u.Query, u.Variable = o.Raw, o.Protocol, o.Query, o.Variable
	u.Host = stringList(o.Host, ".")
	u.Path = stringList(o.Path, "/")
	return nil
}

// stringList reads a string (split on sep) or an array of strings or
// {"value": ...} objects.
func stringList(b json.RawMessage, sep string) []string {
	if len(b) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return strings.Split(strings.Trim(s, sep), sep)
	}
	var raw []json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		var e string
		if json.Unmarshal(r, &e) == nil {
			out = append(out, e)
			continue
		}
		var o struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(r, &o) == nil {
			out = append(out, o.Value)
		}
	}
	return out
}

// Body is a request body.
type Body struct {
	Mode       string `json:"mode"`
	Raw        string `json:"raw"`
	URLEncoded []KV   `json:"urlencoded"`
	FormData   []KV   `json:"formdata"`
	File       struct {
		Src string `json:"src"`
	} `json:"file"`
	GraphQL struct {
		Query     string    `json:"query"`
		Variables flexValue `json:"variables"`
	} `json:"graphql"`
	Options struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
	Disabled bool `json:"disabled"`
}

// Auth is an auth block; its parameters are under the key named by Type.
type Auth struct {
	Type   string
	Params map[string]string
}

// UnmarshalJSON reads {"type": "bearer", "bearer": [{"key": ..., "value": ...}]}
// (v2.1) and {"type": "bearer", "bearer": {"token": ...}} (v2.0).
func (a *Auth) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	_ = json.Unmarshal(m["type"], &a.Type)
	a.Params = map[string]string{}
	params := m[a.Type]
	var list []KV
	if json.Unmarshal(params, &list) == nil {
		for _, kv := range list {
			a.Params[kv.Key] = kv.Value.String()
		}
		return nil
	}
	var obj map[string]flexValue
	if json.Unmarshal(params, &obj) == nil {
		for k, v := range obj {
			a.Params[k] = v.String()
		}
	}
	return nil
}

// Event is a script: Listen is "prerequest" or "test".
type Event struct {
	Listen string `json:"listen"`
	Script struct {
		Exec flexLines `json:"exec"`
	} `json:"script"`
	Disabled bool `json:"disabled"`
}

// Code returns the script's source.
func (e Event) Code() string { return strings.Join(e.Script.Exec, "\n") }

// Variable is a collection variable.
type Variable struct {
	Key      string    `json:"key"`
	Value    flexValue `json:"value"`
	Type     string    `json:"type"`
	Disabled bool      `json:"disabled"`
}

// Environment is a Postman environment file.
type Environment struct {
	Name   string `json:"name"`
	Values []struct {
		Key     string    `json:"key"`
		Value   flexValue `json:"value"`
		Type    string    `json:"type"`
		Enabled *bool     `json:"enabled"`
	} `json:"values"`
}

// flexValue is a JSON value read as text: strings as they are, other
// values as their JSON.
type flexValue struct {
	s   string
	set bool
}

func (v *flexValue) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		v.s, v.set = "", false // "key" without a value
		return nil
	}
	v.set = true
	var s string
	if json.Unmarshal(b, &s) == nil {
		v.s = s
		return nil
	}
	v.s = string(b)
	return nil
}

func (v flexValue) String() string { return v.s }

// flexSrc is a form-data file source: a string or a list (first is used).
type flexSrc string

func (s *flexSrc) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*s = flexSrc(one)
		return nil
	}
	var many []string
	if json.Unmarshal(b, &many) == nil && len(many) > 0 {
		*s = flexSrc(many[0])
	}
	return nil
}

// flexLines is a script's exec: a string or a list of lines.
type flexLines []string

func (l *flexLines) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*l = strings.Split(one, "\n")
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// ParseCollection decodes a collection and checks it is a v2.x collection.
func ParseCollection(data []byte) (*Collection, error) {
	var c Collection
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("not a Postman collection: %w", err)
	}
	if c.Info.Schema == "" && c.Info.Name == "" {
		return nil, fmt.Errorf("not a Postman collection (no info section)")
	}
	if s := c.Info.Schema; s != "" && !strings.Contains(s, "/v2.0.") && !strings.Contains(s, "/v2.1.") {
		return nil, fmt.Errorf("unsupported collection schema %s (export as Collection v2.1)", s)
	}
	return &c, nil
}

// ParseEnvironment decodes an environment file.
func ParseEnvironment(data []byte) (*Environment, error) {
	var e Environment
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("not a Postman environment: %w", err)
	}
	if e.Name == "" && e.Values == nil {
		return nil, fmt.Errorf("not a Postman environment (no name or values)")
	}
	return &e, nil
}
