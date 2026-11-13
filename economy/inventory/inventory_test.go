package inventory

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalTag(t *testing.T) {
	expected := Tag{
		InternalName: "x",
		CategoryName: "y",
	}
	data := []byte(`{"internal_name":"x","category_name":"y"}`)
	got := Tag{}
	err := json.Unmarshal(data, &got)
	if err != nil {
		t.Fatalf("unexpected json error: %v", err)
	}
	if (got.InternalName != expected.InternalName) || (got.CategoryName != expected.CategoryName) {
		t.Fatalf("unmarshaled tag does not match expected data: got=%v, expected=%v", got, expected)
	}
}
