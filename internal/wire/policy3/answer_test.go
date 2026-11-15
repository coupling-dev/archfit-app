package policy3_test

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestLoosens(t *testing.T) {
	tests := []struct {
		kind    policy3.Answer
		want    bool
		wantErr bool
	}{
		{kind: policy3.Approve, want: true},
		{kind: policy3.ApproveUntil, want: true},
		{kind: policy3.Confirm, want: true},
		{kind: policy3.Reject, want: false},
		{kind: "first", wantErr: true},
		{kind: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			got, err := policy3.Loosens(tt.kind)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("Loosens(%q) = %v, %v; want %v, error %v", tt.kind, got, err, tt.want, tt.wantErr)
			}
		})
	}
}
