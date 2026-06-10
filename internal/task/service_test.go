package task

import (
	"strings"
	"testing"

	rt "unified-server/internal/runtime"
)

func TestRewriteTemplateURL_ReplacesNestedFromURL(t *testing.T) {
	raw := "https://mobile.yangkeduo.com/login.html?from=https%3A%2F%2Fmobile.yangkeduo.com%2Forder_checkout.html%3Fgoods_id%3D654097226224%26sku_id%3D1888467061279&refer_page_name=order_checkout"
	got := rewriteTemplateURL(raw, "111222333444", "999888777666")
	if !strings.Contains(got, "goods_id%3D111222333444") {
		t.Fatalf("expected rewritten goods_id in nested url, got %s", got)
	}
	if !strings.Contains(got, "sku_id%3D999888777666") {
		t.Fatalf("expected rewritten sku_id in nested url, got %s", got)
	}
}

func TestRewriteTemplateURL_ReplacesEncodedGoodsListJSON(t *testing.T) {
	raw := "https://mobile.yangkeduo.com/login.html?from=https%3A%2F%2Fmobile.yangkeduo.com%2Ftransac_batch_checkout.html%3Fgoods_list%3D%255B%257B%2522goods_id%2522%253A784768292368%252C%2522sku_id%2522%253A1763658263238%252C%2522goods_number%2522%253A1%257D%255D%26back_page%3Dtransac_batch_checkout&refer_page_name=transac_batch_checkout"
	got := rewriteTemplateURL(raw, "222333444555", "666777888999")
	if !strings.Contains(got, "goods_id%2522%253A222333444555") {
		t.Fatalf("expected rewritten goods_id in goods_list, got %s", got)
	}
	if !strings.Contains(got, "sku_id%2522%253A666777888999") {
		t.Fatalf("expected rewritten sku_id in goods_list, got %s", got)
	}
}

func TestBuildTaskURL_UsesDefaultCheckoutURL(t *testing.T) {
	got := buildTaskURL(clientSystemConfig(), clientTaskItem{GoodsID: "123456", SKUID: "6812323"})
	want := "https://mobile.yangkeduo.com/order_checkout.html?goods_id=123456&sku_id=6812323"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func clientSystemConfig() rt.SystemConfig {
	return rt.SystemConfig{}
}
