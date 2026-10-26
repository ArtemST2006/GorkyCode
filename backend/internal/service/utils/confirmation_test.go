package utils

import "testing"

func TestParseConfirmation(t *testing.T) {
	for _, tc := range []struct {
		text string
		want Confirmation
	}{
		{"да", ConfirmationYes},
		{"Да, ставь", ConfirmationYes},
		{"  ДА!  ", ConfirmationYes},
		{"конечно", ConfirmationYes},
		{"ок", ConfirmationYes},
		{"да, но завтра", ConfirmationUnknown},
		{"да?", ConfirmationUnknown},
		{"да, не ставь", ConfirmationUnknown},
		{"поставь на 17:00", ConfirmationUnknown},
		{"нет", ConfirmationNo},
		{"Нет, не надо", ConfirmationNo},
		{"не ставь", ConfirmationNo},
		{"отмена", ConfirmationNo},
		{"", ConfirmationUnknown},
		{"!!!", ConfirmationUnknown},
		{"а во сколько у меня встреча?", ConfirmationUnknown},
		{"данные", ConfirmationUnknown},
		{"нетушки", ConfirmationUnknown},
	} {
		if got := ParseConfirmation(tc.text); got != tc.want {
			t.Errorf("ParseConfirmation(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
