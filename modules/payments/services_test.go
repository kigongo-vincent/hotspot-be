package payments

import (
	"testing"

	"github.com/kigongo-vincent/hotspot-be/modules/shared"
)

func TestApplyTopupBalance(t *testing.T) {
	t.Run("credits amount and fills currency when empty", func(t *testing.T) {
		got := ApplyTopupBalance(shared.Balance{Value: 100, Currency: ""}, 50.4, "UGX")
		if got.Value != 150 {
			t.Fatalf("value = %d, want 150", got.Value)
		}
		if got.Currency != shared.UGX {
			t.Fatalf("currency = %s, want UGX", got.Currency)
		}
	})
	t.Run("preserves existing currency", func(t *testing.T) {
		got := ApplyTopupBalance(shared.Balance{Value: 100, Currency: shared.UGX}, 50, "USD")
		if got.Currency != shared.UGX {
			t.Fatalf("currency = %s, want UGX", got.Currency)
		}
	})
	t.Run("rounds fractional amounts", func(t *testing.T) {
		got := ApplyTopupBalance(shared.Balance{Value: 0, Currency: shared.UGX}, 99.6, "")
		if got.Value != 100 {
			t.Fatalf("value = %d, want 100", got.Value)
		}
	})
}

func TestIsTransactionNotFoundMessage(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"No transaction was found for this id", true},
		{"no transaction was found for this id", true},
		{"  No Transaction Was Found for this ID  ", true},
		{"verification failed", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsTransactionNotFoundMessage(c.msg); got != c.want {
			t.Errorf("IsTransactionNotFoundMessage(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}