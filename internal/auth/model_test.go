package auth

import "testing"

func TestPermissionGlobal(t *testing.T) {
	tests := []struct {
		p    Permission
		want bool
	}{
		{PermView, false},
		{PermOperate, false},
		{PermInventoryEdit, false},
		{PermRuleEdit, false},
		{PermSourceEdit, true},
		{PermPresetRun, true},
		{PermPresetEdit, true},
		{PermAdmin, true},
	}
	for _, tt := range tests {
		if got := tt.p.Global(); got != tt.want {
			t.Errorf("%s.Global() = %v, want %v", tt.p, got, tt.want)
		}
	}
}
