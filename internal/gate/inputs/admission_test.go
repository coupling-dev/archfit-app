package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
)

func TestAdmitted(t *testing.T) {
	const installationID = 77
	record, _ := json.Marshal(Admission{Account: "octo-labs", By: "alexei-led", ByID: 1898375, At: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)})
	tests := []struct {
		name    string
		auto    []string
		owner   string
		get     func(context.Context) ([]byte, error)
		want    bool
		wantErr bool
	}{
		{"configured account, no read", []string{"alexei-led", " vladikk "}, "vladikk", nil, true, false},
		{"configured account in another case", []string{"Coupling-Dev"}, "coupling-dev", nil, true, false},
		{"operator's record", nil, "octo-labs", func(context.Context) ([]byte, error) { return record, nil }, true, false},
		{"no record", nil, "octo-labs", func(context.Context) ([]byte, error) { return nil, store.ErrNotFound }, false, false},
		{"store fails closed, with an error to retry", nil, "octo-labs", func(context.Context) ([]byte, error) { return nil, errors.New("down") }, false, true},
		{"record that does not decode", nil, "octo-labs", func(context.Context) ([]byte, error) { return []byte("{"), nil }, false, true},
		{"record without the operator's ID", nil, "octo-labs", func(context.Context) ([]byte, error) { return []byte("{}"), nil }, false, true},
		{"null record", nil, "octo-labs", func(context.Context) ([]byte, error) { return []byte("null"), nil }, false, true},
		{"empty configured login admits nobody", []string{""}, "", func(context.Context) ([]byte, error) { return nil, store.ErrNotFound }, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &stubStore{get: tt.get}
			if tt.get == nil {
				s.get = func(context.Context) ([]byte, error) {
					t.Error("a configured account read the Store")
					return nil, store.ErrNotFound
				}
			}
			got, err := NewAdmissions(s, tt.auto).Admitted(context.Background(), installationID, tt.owner)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("Admitted = %t, %v; want %t, error %t", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestAdmissionRecordTellsMissingFromUnreadable(t *testing.T) {
	a := NewAdmissions(&stubStore{get: func(context.Context) ([]byte, error) { return nil, store.ErrNotFound }}, nil)
	if _, found, err := a.Record(context.Background(), 5); found || err != nil {
		t.Errorf("missing record = %t, %v; want false, nil", found, err)
	}
	a = NewAdmissions(&stubStore{get: func(context.Context) ([]byte, error) { return nil, errors.New("down") }}, nil)
	if _, found, err := a.Record(context.Background(), 5); found || err == nil {
		t.Errorf("failing Store = %t, %v; want false and an error", found, err)
	}
}

func TestOwner(t *testing.T) {
	for in, want := range map[string]string{"acme/shop": "acme", "acme": "acme", "": ""} {
		if got := Owner(in); got != want {
			t.Errorf("Owner(%q) = %q, want %q", in, got, want)
		}
	}
}
