package config

import "testing"

func TestValidate(t *testing.T) {
	services := []string{"tripmap"}
	ok := func() Config {
		return Config{Upstreams: map[string]string{"tripmap": "http://tripmap:18081"}, CORSAllowedOrigins: []string{"https://trip.example.com"}}
	}
	if c := ok(); c.Validate(services) != nil {
		t.Fatal(c.Validate(services))
	}
	for name, mutate := range map[string]func(*Config){
		"missing upstream":    func(c *Config) { delete(c.Upstreams, "tripmap") },
		"unknown service":     func(c *Config) { c.Upstreams["tirpmap"] = "http://tripmap:18081" },
		"relative URL":        func(c *Config) { c.Upstreams["tripmap"] = "tripmap:18081" },
		"URL with path":       func(c *Config) { c.Upstreams["tripmap"] = "http://tripmap:18081/api" },
		"URL with query":      func(c *Config) { c.Upstreams["tripmap"] = "http://tripmap:18081?x=1" },
		"URL with password":   func(c *Config) { c.Upstreams["tripmap"] = "http://u:p@tripmap:18081" },
		"origin with a slash": func(c *Config) { c.CORSAllowedOrigins = []string{"https://trip.example.com/"} },
	} {
		c := ok()
		mutate(&c)
		if c.Validate(services) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
