package i18n

import (
	"errors"
	"fmt"
	"testing"
)

func TestNotificationContextPreservesErrorChainAndCopiesParameters(t *testing.T) {
	sentinel := errors.New("operator-diagnostic")
	original := &Error{Code: "api.notification_field_required", Params: map[string]any{"channel": "Email", "field": "password"}, Detail: "safe-detail", Cause: sentinel}
	wrapper := fmt.Errorf("adapter: %w", original)
	result := WithNotificationChannel(wrapper, "Email", "Family {reason} 邮箱")
	var got *Error
	if !errors.As(result, &got) || !errors.Is(result, sentinel) || !errors.Is(result, original) || !errors.Is(result, wrapper) {
		t.Fatal("chain lost")
	}
	if got == original || got.Code != original.Code || got.Detail != original.Detail || original.Context != nil {
		t.Fatal("mutated original or metadata lost")
	}
	got.Params["field"] = "changed"
	if original.Params["field"] != "password" {
		t.Fatal("mutable params shared")
	}
}
