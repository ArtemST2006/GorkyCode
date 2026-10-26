package utils

import (
	"strings"
	"unicode"
)

// Confirmation is the user's answer to a yes/no question.
type Confirmation int

const (
	// ConfirmationUnknown means the answer is neither yes nor no.
	ConfirmationUnknown Confirmation = iota
	// ConfirmationYes means the user agreed.
	ConfirmationYes
	// ConfirmationNo means the user refused.
	ConfirmationNo
)

var (
	yesWords = map[string]bool{
		"да": true, "ага": true, "угу": true, "конечно": true, "давай": true, "ставь": true,
		"поставь": true, "ок": true, "окей": true, "хорошо": true, "подтверждаю": true, "yes": true, "ok": true,
	}
	noWords = map[string]bool{
		"нет": true, "не": true, "неа": true, "отмена": true, "отмени": true, "отменить": true,
		"no": true, "cancel": true,
	}
)

// ParseConfirmation recognises a short yes/no answer by its first word, e.g.
// «Да, ставь» or «нет, не надо». The check runs in the backend because the
// AI contract has no intent for confirmations.
func ParseConfirmation(text string) Confirmation {
	if strings.ContainsAny(text, "?0123456789") {
		return ConfirmationUnknown
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	if len(words) == 0 {
		return ConfirmationUnknown
	}
	switch {
	case yesWords[words[0]]:
		for _, word := range words[1:] {
			if !yesWords[word] && word != "всё" && word != "все" && word != "равно" && word != "пожалуйста" {
				return ConfirmationUnknown
			}
		}
		return ConfirmationYes
	case noWords[words[0]]:
		return ConfirmationNo
	default:
		return ConfirmationUnknown
	}
}
