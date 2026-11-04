package runner

import (
	"context"
	"fmt"
	"net/http"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/pkg/net"
)

type Opts struct {
	HeaderOnly bool
	// Quiet hides response headers and bodies in the console report.
	Quiet bool
	// Bail stops the run after the first failed request (or file).
	Bail bool
	// Filters are globs (* = any text, ? = one character); when set, only
	// requests whose @name, ### title or "METHOD URL" matches one of them run.
	Filters []string
	// Env selects an environment from monica.yml ("" = defaults only).
	Env string
	// Vars are --var overrides; they win over config variables.
	Vars map[string]string
	// ConfigPath is an explicit config file; "" means look for monica.yml
	// above each .mon file.
	ConfigPath string
	// LookupEnv reads OS environment variables for ${NAME}; nil means
	// os.LookupEnv.
	LookupEnv config.LookupFunc
	// Context, when set, stops the run once it is done: the request being
	// sent is aborted and no further request starts. nil means never.
	Context context.Context
	// NoSummary keeps no per-request results for Finish's summary, so a long
	// run (monica load) does not grow; the reporter still gets every result.
	NoSummary bool
	// VU is monica.vu in scripts: the 1-based virtual user under monica
	// load, 0 otherwise.
	VU int
	// Insecure skips TLS certificate verification for every request, as if
	// insecureSkipVerify were set in every config (--insecure).
	Insecure bool
	// ClientCert and ClientKey (--cert, --key) replace monica.yml's
	// tls.clientCert and tls.clientKey; set both or neither. CACert
	// (--cacert) replaces tls.caCert. "" keeps the config's.
	ClientCert, ClientKey, CACert string
	// Proxy (--proxy) replaces monica.yml's proxy: a proxy URL, or "none"
	// for no proxy; "" keeps the config's.
	Proxy string
	// Transports, when set, is the connection pool HTTP clients use
	// (shared between runners under monica load); nil gives each project
	// its own.
	Transports *net.Transports
	// CookieJar, when set, keeps cookies between requests (one per VU
	// under monica load); nil keeps none.
	CookieJar http.CookieJar
}

// CheckTransport validates the --cert, --key, --cacert and --proxy
// options and reads their files, so mistakes fail before anything is sent.
// Settings from monica.yml are checked when each project is loaded.
func (o *Opts) CheckTransport() error {
	switch {
	case o.ClientCert != "" && o.ClientKey == "":
		return fmt.Errorf("--cert %s needs --key", o.ClientCert)
	case o.ClientKey != "" && o.ClientCert == "":
		return fmt.Errorf("--key %s needs --cert", o.ClientKey)
	}
	if o.Proxy != "" {
		if _, err := config.ParseProxy(o.Proxy); err != nil {
			return fmt.Errorf("--proxy: %w", err)
		}
	}
	cfg := net.TransportConfig{ClientCert: o.ClientCert, ClientKey: o.ClientKey, CACert: o.CACert}
	return cfg.Check()
}

// transportConfig applies the options over a project's settings.
func (o *Opts) transportConfig(s *config.Resolved) (net.TransportConfig, error) {
	cfg := net.TransportConfig{
		InsecureSkipVerify: s.InsecureSkipVerify || o.Insecure,
		ClientCert:         s.ClientCert,
		ClientKey:          s.ClientKey,
		CACert:             s.CACert,
		Proxy:              s.Proxy,
	}
	if o.ClientCert != "" || o.ClientKey != "" {
		cfg.ClientCert, cfg.ClientKey = o.ClientCert, o.ClientKey
	}
	if o.CACert != "" {
		cfg.CACert = o.CACert
	}
	if o.Proxy != "" {
		p, err := config.ParseProxy(o.Proxy)
		if err != nil {
			return cfg, fmt.Errorf("--proxy: %w", err)
		}
		cfg.Proxy = p
	}
	return cfg, nil
}
