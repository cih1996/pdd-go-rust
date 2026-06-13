package device

import "testing"

func TestEscapeADBURL_EscapesShellSensitiveCharacters(t *testing.T) {
	raw := `https://mobile.yangkeduo.com/goods.html?goods_id=123&sku_id=456&name=a b(test)`
	got := escapeADBURL(raw)
	want := `https://mobile.yangkeduo.com/goods.html?goods_id=123\&sku_id=456\&name=a\ b\(test\)`
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}
