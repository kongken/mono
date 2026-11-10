package application

import (
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"testing"
)

func TestValidateEntry(t *testing.T) {
	base := &timelinev1.Entry{Title: "Dinner", Date: "2026-11-07", Timezone: "Asia/Shanghai"}
	if err := validate(base, false); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	for name, in := range map[string]*timelinev1.Entry{
		"missing title": {Date: base.Date, Timezone: base.Timezone},
		"bad date":      {Title: base.Title, Date: "2026-02-30", Timezone: base.Timezone},
		"bad timezone":  {Title: base.Title, Date: base.Date, Timezone: "Mars/Base"},
		"bad scheme":    {Title: base.Title, Date: base.Date, Timezone: base.Timezone, Url: "javascript:alert(1)"},
		"private host":  {Title: base.Title, Date: base.Date, Timezone: base.Timezone, Url: "http://127.0.0.1/admin"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(in, false); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
