// deploycheck validates rendered deployment invariants, not cloud runtime readiness.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

type resource struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Provisioner   string `yaml:"provisioner"`
	ReclaimPolicy string `yaml:"reclaimPolicy"`
	Spec          struct {
		Type     string `yaml:"type"`
		Replicas int    `yaml:"replicas"`
		Strategy struct {
			Type string `yaml:"type"`
		} `yaml:"strategy"`
		AccessModes      []string `yaml:"accessModes"`
		StorageClassName string   `yaml:"storageClassName"`
		Template         struct {
			Spec struct {
				AutomountServiceAccountToken  bool `yaml:"automountServiceAccountToken"`
				TerminationGracePeriodSeconds int  `yaml:"terminationGracePeriodSeconds"`
				SecurityContext               struct {
					RunAsNonRoot bool `yaml:"runAsNonRoot"`
					RunAsUser    int  `yaml:"runAsUser"`
					FSGroup      int  `yaml:"fsGroup"`
				} `yaml:"securityContext"`
				Containers []struct {
					Name    string `yaml:"name"`
					EnvFrom []struct {
						SecretRef struct {
							Name string `yaml:"name"`
						} `yaml:"secretRef"`
					} `yaml:"envFrom"`
					Env []struct {
						Name  string `yaml:"name"`
						Value string `yaml:"value"`
					} `yaml:"env"`
					SecurityContext struct {
						AllowPrivilegeEscalation bool `yaml:"allowPrivilegeEscalation"`
						ReadOnlyRootFilesystem   bool `yaml:"readOnlyRootFilesystem"`
					} `yaml:"securityContext"`
					VolumeMounts []struct {
						Name      string `yaml:"name"`
						MountPath string `yaml:"mountPath"`
					} `yaml:"volumeMounts"`
				} `yaml:"containers"`
				Volumes []struct {
					Name                  string `yaml:"name"`
					PersistentVolumeClaim struct {
						ClaimName string `yaml:"claimName"`
					} `yaml:"persistentVolumeClaim"`
				} `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func validate(data []byte, provider string) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	deployments, claims, services := map[string]bool{}, map[string]bool{}, map[string]bool{}
	storageClass := ""
	for {
		var r resource
		if err := decoder.Decode(&r); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		name := r.Metadata.Name
		switch r.Kind {
		case "StorageClass":
			expected := "ebs.csi.aws.com"
			if provider == "google-cloud" {
				expected = "pd.csi.storage.gke.io"
			}
			if r.Provisioner != expected || r.ReclaimPolicy != "Retain" {
				return errors.New("provider storage must retain state")
			}
			storageClass = name
		case "PersistentVolumeClaim":
			if claims[name] || len(r.Spec.AccessModes) != 1 || r.Spec.AccessModes[0] != "ReadWriteOnce" || r.Spec.StorageClassName != "wakeplane-"+provider {
				return fmt.Errorf("invalid durable claim %s", name)
			}
			claims[name] = true
		case "Service":
			if services[name] || r.Spec.Type != "ClusterIP" {
				return fmt.Errorf("service %s must remain private", name)
			}
			services[name] = true
		case "Deployment":
			pod := r.Spec.Template.Spec
			if deployments[name] || r.Spec.Replicas != 1 || r.Spec.Strategy.Type != "Recreate" {
				return fmt.Errorf("%s must be one replica without deployment overlap", name)
			}
			deployments[name] = true
			if pod.AutomountServiceAccountToken || !pod.SecurityContext.RunAsNonRoot || pod.SecurityContext.RunAsUser != 10001 || pod.SecurityContext.FSGroup != 10001 || pod.TerminationGracePeriodSeconds < 30 {
				return fmt.Errorf("unsafe pod security or drain %s", name)
			}
			if len(pod.Containers) != 1 {
				return errors.New("daemon and runner must remain separate")
			}
			c := pod.Containers[0]
			if c.SecurityContext.AllowPrivilegeEscalation || !c.SecurityContext.ReadOnlyRootFilesystem || len(c.EnvFrom) != 1 || c.EnvFrom[0].SecretRef.Name != name+"-secrets" {
				return errors.New("unsafe container or missing secret reference")
			}
			mount, claim := false, false
			for _, v := range c.VolumeMounts {
				if v.Name == "state" && v.MountPath == "/data" {
					mount = true
				}
			}
			for _, v := range pod.Volumes {
				if v.Name == "state" && v.PersistentVolumeClaim.ClaimName == name+"-state" {
					claim = true
				}
			}
			if !mount || !claim {
				return fmt.Errorf("missing durable state for %s", name)
			}
			if name == "runner" {
				found := false
				for _, e := range c.Env {
					if e.Name == "AUTOMATION_RUNNER_PUBLIC_URL" && e.Value == "http://runner.wakeplane.svc.cluster.local:8080" {
						found = true
					}
				}
				if !found {
					return errors.New("runner origin must be private and match its service")
				}
			}
		case "Namespace":
		default:
			return fmt.Errorf("unreviewed resource kind %s", r.Kind)
		}
	}
	if storageClass != "wakeplane-"+provider || len(deployments) != 2 || len(claims) != 2 || len(services) != 2 {
		return errors.New("incomplete topology")
	}
	for _, name := range []string{"daemon", "runner"} {
		if !deployments[name] || !claims[name+"-state"] || !services[name] {
			return errors.New("missing daemon or runner resources")
		}
	}
	return nil
}

func main() {
	for _, provider := range []string{"aws", "google-cloud"} {
		cmd := exec.Command("kubectl", "kustomize", "infra/kubernetes/"+provider)
		data, err := cmd.Output()
		if err == nil {
			err = validate(data, provider)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, provider+": "+err.Error())
			os.Exit(1)
		}
		fmt.Println(strings.ToUpper(provider) + " deployment invariants PASS")
	}
}
