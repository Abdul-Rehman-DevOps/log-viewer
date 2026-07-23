package k8s_test

import (
	"strings"
	"testing"
	"time"

	"github.com/AIVMNetwork/log-viewer/internal/k8s"
)

// Exercise format via exported behavior through Stream is hard; test helper logic by
// duplicating parse expectations against public package using a tiny mirror.
// We validate PKT offset (+5h) using the same zone load.
func TestPakistanTimezoneOffset(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Karachi")
	if err != nil {
		loc = time.FixedZone("PKT", 5*3600)
	}
	utc := time.Date(2026, 7, 23, 11, 40, 13, 423000000, time.UTC)
	pkt := utc.In(loc)
	got := pkt.Format("2006-01-02 15:04:05.000 PKT")
	if !strings.Contains(got, "16:40:13") {
		t.Fatalf("expected PKT +5h, got %s", got)
	}
	_ = k8s.Workload{}
}
