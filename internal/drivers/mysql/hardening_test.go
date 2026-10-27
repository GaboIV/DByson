package mysql

import "testing"

func TestRequireExactPK(t *testing.T) {
	pk := []string{"id"}
	if err := requireExactPK(pk, map[string]any{"id": 5}); err != nil {
		t.Fatalf("exact single key rejected: %v", err)
	}
	// A non-key column must not stand in for the primary key: it could match many rows.
	if requireExactPK(pk, map[string]any{"tenant_id": 5}) == nil {
		t.Fatal("a non-key column was accepted as the primary key")
	}
	// Extra or missing columns on a composite key are rejected.
	comp := []string{"a", "b"}
	if requireExactPK(comp, map[string]any{"a": 1}) == nil {
		t.Fatal("a partial composite key was accepted")
	}
	if requireExactPK(comp, map[string]any{"a": 1, "b": 2, "c": 3}) == nil {
		t.Fatal("an over-specified key was accepted")
	}
	if requireExactPK(comp, map[string]any{"a": 1, "b": nil}) == nil {
		t.Fatal("a nil key value was accepted")
	}
	if err := requireExactPK(comp, map[string]any{"a": 1, "b": 2}); err != nil {
		t.Fatalf("exact composite key rejected: %v", err)
	}
}

func TestCSVSafeField(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1":           "'=1+1",
		"@SUM(A1)":       "'@SUM(A1)",
		"+cmd|' /C calc": "'+cmd|' /C calc",
		"-2+3":           "'-2+3",
		"\t=danger":      "'\t=danger",
		"-5":             "-5",    // a plain negative number stays
		"+7":             "+7",    // a signed number stays
		"hello":          "hello", // ordinary text is untouched
		"":               "",
		"a=1":            "a=1", // only the first character matters
	} {
		if got := csvSafeField(in); got != want {
			t.Errorf("csvSafeField(%q) = %q, want %q", in, got, want)
		}
	}
}
