package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestRenderedDeploymentInvariants(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl required; CI installs pinned kubectl")
	}
	for _, provider := range []string{"aws", "google-cloud"} {
		t.Run(provider, func(t *testing.T) {
			data, err := exec.Command("kubectl", "kustomize", "../../infra/kubernetes/"+provider).Output()
			if err != nil {
				t.Fatal(err)
			}
			if err = validate(data, provider); err != nil {
				t.Fatal(err)
			}
			for _, replacement := range [][2]string{{"replicas: 1", "replicas: 2"}, {"type: Recreate", "type: RollingUpdate"}, {"type: ClusterIP", "type: LoadBalancer"}, {"reclaimPolicy: Retain", "reclaimPolicy: Delete"}, {"claimName: runner-state", "claimName: daemon-state"}, {"runAsNonRoot: true", "runAsNonRoot: false"}} {
				changed := strings.Replace(string(data), replacement[0], replacement[1], 1)
				if changed == string(data) {
					t.Fatalf("fixture fragment absent: %s", replacement[0])
				}
				if validate([]byte(changed), provider) == nil {
					t.Fatalf("unsafe mutation accepted: %s", replacement[1])
				}
			}
		})
	}
}
