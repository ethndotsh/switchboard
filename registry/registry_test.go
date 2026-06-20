package registry

import "testing"

func TestValidateNamespace(t *testing.T) {
	valid := []string{"", "customer-a", "customer_a/prod", "team.1/edge-2"}
	for _, namespace := range valid {
		if err := ValidateNamespace(namespace); err != nil {
			t.Fatalf("expected namespace %q to pass: %v", namespace, err)
		}
	}

	invalid := []string{"/customer", "customer/", "customer//prod", ".", "..", "customer/../prod", "customer prod", "customer:prod"}
	for _, namespace := range invalid {
		if err := ValidateNamespace(namespace); err == nil {
			t.Fatalf("expected namespace %q to fail", namespace)
		}
	}
}

func TestS3ScopedKey(t *testing.T) {
	reg := &S3Registry{prefix: "base"}
	if got := reg.scopedKey(Scope{}, "channels", "prod.json"); got != "base/channels/prod.json" {
		t.Fatalf("global key = %q", got)
	}
	if got := reg.scopedKey(Scope{Namespace: "customer-a/prod"}, "channels", "prod.json"); got != "base/namespaces/customer-a/prod/channels/prod.json" {
		t.Fatalf("namespaced key = %q", got)
	}
}
