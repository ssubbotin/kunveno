package auth

import "testing"

func TestRuleAllows(t *testing.T) {
	r := Rule{"*@example.com", "boss@example.org"}
	for login, want := range map[string]bool{
		"ann@example.com":          true,
		"ANN@Example.COM":          true, // logins compare case-insensitively
		"boss@example.org":         true,
		"other@example.org":        false,
		"ann":                      false, // a personal Yandex account: no domain
		"ann@example.com.evil.org": false,
		"ann@sub.example.com":      false,
		"":                         false,
	} {
		if got := r.Allows(login); got != want {
			t.Errorf("Allows(%q) = %v, want %v", login, got, want)
		}
	}
	if (Rule{}).Allows("ann@example.com") {
		t.Error("an empty rule allowed someone")
	}
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[Identity]string{
		{Login: "ann@example.com", Name: "  Анна Иванова "}:                            "Анна Иванова",
		{Login: "ann@example.com", Name: ""}:                                           "ann",
		{Login: "ann@example.com", Name: "Очень длинное имя сотрудника с отчеством И"}: "Очень длинное имя сотрудника с о",
	} {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%+v) = %q, want %q", in, got, want)
		}
	}
}
