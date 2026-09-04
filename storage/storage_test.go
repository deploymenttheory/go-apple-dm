package storage_test

import (
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
)

func TestNotNowBackoff(t *testing.T) {
	t.Parallel()
	cases := map[int]time.Duration{-1: 30 * time.Second, 0: 30 * time.Second, 1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 8: time.Hour, 20: time.Hour}
	for n, want := range cases {
		if got := storage.NotNowBackoff(n); got != want {
			t.Errorf("NotNowBackoff(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestStateTerminal(t *testing.T) {
	t.Parallel()
	for s, want := range map[storage.State]bool{storage.StatePending: false, storage.StateSent: false, storage.StateNotNow: false, storage.StateAcknowledged: true, storage.StateError: true, storage.StateCleared: true} {
		if s.Terminal() != want {
			t.Errorf("%s.Terminal() = %v", s, s.Terminal())
		}
	}
}

func TestDeviceInfoFromAuthenticate(t *testing.T) {
	t.Parallel()
	if d := storage.DeviceInfoFromAuthenticate(nil); d != (storage.DeviceInfo{}) {
		t.Error("nil should give zero")
	}
	serial := "C02"
	d := storage.DeviceInfoFromAuthenticate(&checkin.Authenticate{Topic: "t", Model: "m", ModelName: "mn", DeviceName: "dn", SerialNumber: &serial})
	if d.Topic != "t" || d.Model != "m" || d.ModelName != "mn" || d.DeviceName != "dn" || d.SerialNumber != "C02" || d.OSVersion != "" {
		t.Errorf("DeviceInfo = %+v", d)
	}
}
