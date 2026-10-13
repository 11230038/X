package notebook

// Categories are stored beside a question rather than in it, and this is what
// that costs and buys: the name is the identity, so two questions filed under
// one name share one row, and filing a question under a name it already carries
// is a no-op the database has to be asked to confirm.

import (
	"slices"
	"testing"
)

// TestAddCategoriesFilesUnderOneName walks what a category is: one row shared by
// the questions filed under it, created under a key derived from its name so
// that two writers cannot make two of it.
func TestAddCategoriesFilesUnderOneName(t *testing.T) {
	service, resource := newDBService(t)
	first := save(t, service, withCategories(sampleQuestion("什么是闭包"), "数学", " 数学 ", "数学"))

	if count := countRows(t, resource, "notebook_categories", ""); count != 1 {
		t.Errorf("the same name three times made %d categories, want one", count)
	}
	if count := countRows(t, resource, "notebook_entry_categories", ""); count != 1 {
		t.Errorf("the same name three times made %d filings, want one", count)
	}

	second := save(t, service, withCategories(sampleQuestion("什么是作用域"), "数学", "英语"))
	if count := countRows(t, resource, "notebook_categories", ""); count != 2 {
		t.Errorf("the bank holds %d categories, want the two distinct names", count)
	}
	if count := countRows(t, resource, "notebook_entry_categories", ""); count != 3 {
		t.Errorf("there are %d filings, want three: two questions, two names, one shared", count)
	}
	if got := entryOf(t, service, second.QuestionID).Categories; !slices.Equal(got, []string{"数学", "英语"}) {
		t.Errorf("the second question is filed under %q, want [数学 英语]", got)
	}
	if got := entryOf(t, service, first.QuestionID).Categories; !slices.Equal(got, []string{"数学"}) {
		t.Errorf("the first question is filed under %q, want [数学]", got)
	}
}

// TestAddCategoriesAddsAndNeverRemoves is what a second generation of a question
// does to the names it is filed under. A category the user put there by hand is
// not something generating the question again gets to drop.
func TestAddCategoriesAddsAndNeverRemoves(t *testing.T) {
	service, _ := newDBService(t)
	first := save(t, service, withCategories(sampleQuestion("什么是闭包"), "数学"))
	save(t, service, withCategories(sampleQuestion("什么是闭包"), "英语"))

	if got := entryOf(t, service, first.QuestionID).Categories; !slices.Equal(got, []string{"数学", "英语"}) {
		t.Errorf("the question is filed under %q, want both names", got)
	}
}

// TestAddCategoriesDropsNamesTheTableWouldRefuse covers the one thing a caller
// can name that cannot be a category: the table has a check constraint against a
// name of nothing but spaces, so such a name is dropped rather than sent to be
// refused.
func TestAddCategoriesDropsNamesTheTableWouldRefuse(t *testing.T) {
	service, resource := newDBService(t)
	save(t, service, withCategories(sampleQuestion("什么是闭包"), "", "  ", "\t", "数学"))

	if count := countRows(t, resource, "notebook_categories", ""); count != 1 {
		t.Errorf("the bank holds %d categories, want only the one with a name", count)
	}
	if count := countRows(t, resource, "notebook_entry_categories", ""); count != 1 {
		t.Errorf("there are %d filings, want only the one", count)
	}
}
