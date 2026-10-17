package persona

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateTrimsTheThreeFields(t *testing.T) {
	cleaned, err := Input{
		Name:        "  耐心的物理老师  ",
		Description: "  适合解释概念  ",
		Content:     "  用类比讲清楚，再给例子。  ",
	}.Validate()
	if err != nil {
		t.Fatalf("a valid input was refused: %v", err)
	}
	if cleaned.Name != "耐心的物理老师" {
		t.Errorf("the name is %q, want it trimmed", cleaned.Name)
	}
	if cleaned.Description != "适合解释概念" {
		t.Errorf("the description is %q, want it trimmed", cleaned.Description)
	}
	if cleaned.Content != "用类比讲清楚，再给例子。" {
		t.Errorf("the instruction is %q, want it trimmed", cleaned.Content)
	}
}

func TestValidateCountsRunesNotBytes(t *testing.T) {
	// Each of these is three bytes in UTF-8 and one rune. A byte count would
	// refuse a name the column accepts, which is the mistake this pins.
	name := strings.Repeat("教", MaxNameRunes)
	content := strings.Repeat("写", MaxContentRunes)
	if _, err := (Input{Name: name, Content: content}).Validate(); err != nil {
		t.Fatalf("an input at the limits was refused: %v", err)
	}

	_, err := (Input{Name: name + "教", Content: content}).Validate()
	if !errors.Is(err, ErrNameTooLong) {
		t.Errorf("one rune over the name limit: %v, want %v", err, ErrNameTooLong)
	}
	_, err = (Input{Name: name, Content: content + "写"}).Validate()
	if !errors.Is(err, ErrContentTooLong) {
		t.Errorf("one rune over the instruction limit: %v, want %v", err, ErrContentTooLong)
	}
}

func TestValidateRefusesWhatThePersonaCannotBe(t *testing.T) {
	cases := []struct {
		name  string
		input Input
		want  error
	}{
		{"no name", Input{Name: "   ", Content: "说点什么"}, ErrNameRequired},
		{"no instruction", Input{Name: "老师", Content: " \n\t "}, ErrContentRequired},
		{
			"long description",
			Input{Name: "老师", Description: strings.Repeat("说", MaxDescriptionRunes+1), Content: "说点什么"},
			ErrDescriptionTooLong,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.input.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("%v, want %v", err, test.want)
			}
		})
	}
}
