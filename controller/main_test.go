package main

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestGetClusterDomain(t *testing.T) {
	c := Config{}
	if c.GetClusterDomain() != "cluster.local" {
		t.Errorf("cluster domain should default to cluster.local, not: %s", c.GetClusterDomain())
	}

	c.ClusterDomain = "mydomain.com"
	if c.GetClusterDomain() != "mydomain.com" {
		t.Errorf("cluster domain should default to cluster.local, not: %s", c.GetClusterDomain())
	}
}

func TestShouldMutate(t *testing.T) {
	testCases := []struct {
		description string
		subject     string
		namespace   string
		expected    bool
	}{
		{"full cluster domain", "test.default.svc.cluster.local", "default", true},
		{"full cluster domain wrong ns", "test.default.svc.cluster.local", "kube-system", false},
		{"left dots get stripped", ".test.default.svc.cluster.local", "default", true},
		{"left dots get stripped wrong ns", ".test.default.svc.cluster.local", "kube-system", false},
		{"right dots get stripped", "test.default.svc.cluster.local.", "default", true},
		{"right dots get stripped wrong ns", "test.default.svc.cluster.local.", "kube-system", false},
		{"dots get stripped", ".test.default.svc.cluster.local.", "default", true},
		{"dots get stripped wrong ns", ".test.default.svc.cluster.local.", "kube-system", false},
		{"partial cluster domain", "test.default.svc.cluster", "default", true},
		{"partial cluster domain wrong ns is still allowed because not valid hostname", "test.default.svc.cluster", "kube-system", true},
		{"service domain", "test.default.svc", "default", true},
		{"service domain wrong ns", "test.default.svc", "kube-system", false},
		{"two part domain", "test.default", "default", true},
		{"two part domain different ns", "test.default", "kube-system", true},
		{"one hostname", "test", "default", true},
		{"no subject specified", "", "default", false},
		{"three part not cluster", "test.default.com", "kube-system", true},
		{"four part not cluster", "test.default.svc.com", "kube-system", true},
		{"five part not cluster", "test.default.svc.cluster.com", "kube-system", true},
		{"six part not cluster", "test.default.svc.cluster.local.com", "kube-system", true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			req := TLSIdentityRequest{
				commonName: testCase.subject,
				duration: "",
			}
			mutationAllowed, validationErr := shouldMutate(req, testCase.namespace, "cluster.local", true)
			if mutationAllowed != testCase.expected {
				t.Errorf("shouldMutate did not return %t for %s", testCase.expected, testCase.description)
			}
			if testCase.subject != "" && mutationAllowed == false && validationErr == nil {
				t.Errorf("shouldMutate should return validation error for invalid hostname")
			}
		})
	}
}

func TestShouldMutateNotRestrictToNamespace(t *testing.T) {
	req := TLSIdentityRequest{
		commonName: "test.default.svc.cluster.local",
		duration: "",
	}
	mutationAllowed, _ := shouldMutate(req, "kube-system", "cluster.local", false)
	if mutationAllowed == false {
		t.Errorf("shouldMutate should return true even with a wrong namespace if restrictToNamespace is false.")
	}
}

func Test_mkRenewer(t *testing.T) {
	type args struct {
		config     *Config
		podName    string
		commonName string
		namespace  string
	}
	tests := []struct {
		name string
		args args
		want corev1.Container
	}{
		{"ok", args{&Config{CaURL: "caURL", ClusterDomain: "clusterDomain"}, "podName", "commonName", "namespace"}, corev1.Container{
			Env: []corev1.EnvVar{
				{Name: "STEP_CA_URL", Value: "caURL"},
				{Name: "COMMON_NAME", Value: "commonName"},
				{Name: "POD_NAME", Value: "podName"},
				{Name: "NAMESPACE", Value: "namespace"},
				{Name: "CLUSTER_DOMAIN", Value: "clusterDomain"},
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := TLSIdentityRequest{
				commonName: tt.args.commonName,
				duration: "",
			}
			if got := req.mkRenewer(tt.args.config, tt.args.podName, tt.args.namespace); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mkRenewer() = %v, want %v", got, tt.want)
			}
		})
	}
}
