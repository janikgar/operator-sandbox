/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"fmt"

	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type DBRole string

const (
	Primary DBRole = "Primary"
	Replica DBRole = "Replica"
)

// PostgresSpec defines the desired state of Postgres
type PostgresSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	//+kubebuilder:validation:Enum=Primary;Replica
	Role   DBRole `json:"role"`
	Source string `json:"source,omitempty"`
}

type TargetStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	UpToDate  bool   `json:"upToDate"`
}

// PostgresStatus defines the observed state of Postgres
type PostgresStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Available    bool           `json:"available"`
	Databases    []string       `json:"databases"`
	Role         DBRole         `json:"role"`
	TargetStatus []TargetStatus `json:"targetStatus"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Postgres is the Schema for the postgres API
type Postgres struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresSpec   `json:"spec,omitempty"`
	Status PostgresStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PostgresList contains a list of Postgres
type PostgresList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Postgres `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Postgres{}, &PostgresList{})
}

func ptr[T any](d T) *T {
	return &d
}

func (pg *Postgres) GenerateSA() *core.ServiceAccount {
	sa := core.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("postgresql-%s", pg.Name),
			Namespace: pg.Namespace,
			Labels: map[string]string{
				"managed-by":    "pgoperator",
				"postgres-name": pg.Name,
			},
		},
	}
	return &sa
}

func (pg *Postgres) GenerateCM() *core.ConfigMap {
	cm := core.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("postgresql-%s-primary-extended-configuration", pg.Name),
			Namespace: pg.Namespace,
			Labels: map[string]string{
				"managed-by":    "pgoperator",
				"postgres-name": pg.Name,
			},
		},
		Data: map[string]string{
			"override.conf": "",
		},
	}
	return &cm
}

func (pg *Postgres) GenerateSecrets() *core.Secret {
	if pg.Spec.Role == Replica {
		return nil
	}
	secret := core.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("postgresql-%s", pg.Name),
			Namespace: pg.Namespace,
			Labels: map[string]string{
				"managed-by":    "pgoperator",
				"postgres-name": pg.Name,
			},
		},
		StringData: map[string]string{
			"ADMIN_PASSWORD":       "admin",
			"REPLICATION_PASSWORD": "repl",
			"USER_PASSWORD":        "user",
		},
	}
	return &secret
}

func (pg *Postgres) GenerateSTS() *apps.StatefulSet {
	chosenSecret := fmt.Sprintf("postgresql-%s", pg.Name)
	if pg.Spec.Role == Replica {
		chosenSecret = fmt.Sprintf("postgresql-%s", pg.Spec.Source)
	}
	sts := apps.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("postgresql-%s", pg.Name),
			Namespace: pg.Namespace,
			Labels: map[string]string{
				"managed-by":    "pgoperator",
				"postgres-name": pg.Name,
			},
		},
		Spec: apps.StatefulSetSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"db.janikgar.lan/component": "pg-statefulset",
					"db.janikgar.lan/instance":  pg.Name,
				},
			},
			Template: core.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: pg.Name,
					Labels: map[string]string{
						"managed-by":                "pgoperator",
						"postgres-name":             pg.Name,
						"db.janikgar.lan/component": "pg-statefulset",
						"db.janikgar.lan/instance":  pg.Name,
					},
				},
				Spec: core.PodSpec{
					Containers: []core.Container{
						{
							Name:  "postgresql",
							Image: "docker.io/bitnami/postgresql:16.2.0-debian-12-r5",
							LivenessProbe: &core.Probe{
								ProbeHandler: core.ProbeHandler{
									Exec: &core.ExecAction{
										Command: []string{
											"/bin/sh",
											"-c",
											`exec pg_isready -U "postgres" -h 127.0.0.1 -p 5432`,
										},
									},
								},
								FailureThreshold:    6,
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								SuccessThreshold:    1,
								TimeoutSeconds:      5,
							},
							ReadinessProbe: &core.Probe{
								ProbeHandler: core.ProbeHandler{
									Exec: &core.ExecAction{
										Command: []string{
											"/bin/sh",
											"-c",
											"-e",
											`exec pg_isready -U \"postgres\" -h 127.0.0.1 -p 5432
[ -f /opt/bitnami/postgresql/tmp/.initialized ] || [ -f /bitnami/postgresql/.initialized ]`,
										},
									},
								},
								FailureThreshold:    6,
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								SuccessThreshold:    1,
								TimeoutSeconds:      5,
							},
							Ports: []core.ContainerPort{{
								ContainerPort: 5432,
								Name:          "tcp-postgresql",
							}},
							Resources: core.ResourceRequirements{
								Limits: core.ResourceList{
									core.ResourceCPU:    resource.MustParse("300m"),
									core.ResourceMemory: resource.MustParse("150Mi"),
								},
								Requests: core.ResourceList{
									core.ResourceCPU:    resource.MustParse("150m"),
									core.ResourceMemory: resource.MustParse("75Mi"),
								},
							},
							SecurityContext: &core.SecurityContext{
								AllowPrivilegeEscalation: ptr(false),
								Capabilities: &core.Capabilities{
									Drop: []core.Capability{"ALL"},
								},
								Privileged:             ptr(false),
								ReadOnlyRootFilesystem: ptr(false),
								RunAsGroup:             ptr(int64(0)),
								RunAsUser:              ptr(int64(1001)),
							},
							VolumeMounts: []core.VolumeMount{
								{
									Name:      "empty-dir",
									MountPath: "/tmp",
									SubPath:   "tmp-dir",
								},
								{
									Name:      "empty-dir",
									MountPath: "/opt/bitnami/postgresql/conf",
									SubPath:   "app-conf-dir",
								},
								{
									Name:      "empty-dir",
									MountPath: "/opt/bitnami/postgresql/tmp",
									SubPath:   "app-tmp-dir",
								},
								{
									Name:      "empty-dir",
									MountPath: "/opt/bitnami/postgresql/logs",
									SubPath:   "app-logs-dir",
								},
								{
									Name:      "postgresql-extended-config",
									MountPath: "/bitnami/postgresql/conf/conf.d/",
								},
								{
									Name:      "dshm",
									MountPath: "/dev/shm",
								},
								{
									Name:      "data",
									MountPath: "/bitnami/postgresql",
								},
							},
							Env: []core.EnvVar{
								{
									Name:  "BITNAMI_DEBUG",
									Value: "false",
								},
								{
									Name:  "POSTGRESQL_PORT_NUMBER",
									Value: "5432",
								},
								{
									Name:  "POSTGRESQL_VOLUME_DIR",
									Value: "/bitnami/postgresql",
								},
								{
									Name:  "PGDATA",
									Value: "/bitnami/postgresql/data",
								},
								{
									Name:  "POSTGRES_REPLICATION_MODE",
									Value: "master",
								},
								{
									Name:  "POSTGRES_REPLICATION_USER",
									Value: "repl_user",
								},
								{
									Name:  "POSTGRES_CLUSTER_APP_NAME",
									Value: "my_application",
								},
								{
									Name:  "POSTGRESQL_ENABLE_LDAP",
									Value: "no",
								},
								{
									Name:  "POSTGRESQL_ENABLE_TLS",
									Value: "no",
								},
								{
									Name:  "POSTGRESQL_LOG_HOSTNAME",
									Value: "false",
								},
								{
									Name:  "POSTGRESQL_LOG_CONNECTIONS",
									Value: "false",
								},
								{
									Name:  "POSTGRESQL_LOG_DISCONNECTIONS",
									Value: "false",
								},
								{
									Name:  "POSTGRESQL_PGAUDIT_LOG_CATALOG",
									Value: "off",
								},
								{
									Name:  "POSTGRESQL_CLIENT_MIN_MESSAGES",
									Value: "error",
								},
								{
									Name:  "POSTGRESQL_SHARED_PRELOAD_LIBRARIES",
									Value: "pgaudit",
								},
								{
									Name: "POSTGRES_PASSWORD",
									ValueFrom: &core.EnvVarSource{
										SecretKeyRef: &core.SecretKeySelector{
											Key: "ADMIN_PASSWORD",
											LocalObjectReference: core.LocalObjectReference{
												Name: chosenSecret,
											},
										},
									},
								},
								{
									Name: "POSTGRES_REPLICATION_PASSWORD",
									ValueFrom: &core.EnvVarSource{
										SecretKeyRef: &core.SecretKeySelector{
											Key: "REPLICATION_PASSWORD",
											LocalObjectReference: core.LocalObjectReference{
												Name: chosenSecret,
											},
										},
									},
								},
							},
						},
					},
					InitContainers: []core.Container{
						{
							Name: "init-chmod-data",
							Command: []string{
								"/bin/sh",
								"-ec",
								`chown 1001:1001 /bitnami/postgresql
mkdir -p /bitnami/postgresql/data
chmod 700 /bitnami/postgresql/data
find /bitnami/postgresql -mindepth 1 -maxdepth 1 -not -name "conf" -not -name ".snapshot" -not -name "lost+found" | \
xargs -r chown -R 1001:1001
chmod -R 777 /dev/shm`,
							},
							Image: "docker.io/bitnami/os-shell:12-debian-12-r16",
							SecurityContext: &core.SecurityContext{
								AllowPrivilegeEscalation: ptr(true),
								Capabilities: &core.Capabilities{
									Drop: []core.Capability{"ALL"},
								},
								Privileged:             ptr(true),
								ReadOnlyRootFilesystem: ptr(false),
								RunAsGroup:             ptr(int64(0)),
								RunAsUser:              ptr(int64(0)),
							},
							VolumeMounts: []core.VolumeMount{
								{
									Name:      "empty-dir",
									MountPath: "/tmp",
									SubPath:   "tmp-dir",
								},
								{
									Name:      "dshm",
									MountPath: "/dev/shm",
								},
								{
									Name:      "data",
									MountPath: "/bitnami/postgresql",
								},
							},
						},
					},
					ServiceAccountName: fmt.Sprintf("postgresql-%s", pg.Name),
					SecurityContext: &core.PodSecurityContext{
						FSGroup:             ptr(int64(1001)),
						FSGroupChangePolicy: ptr(core.FSGroupChangeAlways),
					},
					Volumes: []core.Volume{
						{
							Name: "empty-dir",
							VolumeSource: core.VolumeSource{
								EmptyDir: &core.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "postgresql-extended-config",
							VolumeSource: core.VolumeSource{
								ConfigMap: &core.ConfigMapVolumeSource{
									DefaultMode: ptr(int32(420)),
									LocalObjectReference: core.LocalObjectReference{
										Name: fmt.Sprintf("postgresql-%s-primary-extended-configuration", pg.Name),
									},
								},
							},
						},
						{
							Name: "dshm",
							VolumeSource: core.VolumeSource{
								EmptyDir: &core.EmptyDirVolumeSource{
									Medium: core.StorageMediumMemory,
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []core.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{
					Name: "data",
				},
				Spec: core.PersistentVolumeClaimSpec{
					AccessModes: []core.PersistentVolumeAccessMode{core.ReadWriteOnce},
					Resources: core.VolumeResourceRequirements{
						Requests: core.ResourceList{
							core.ResourceStorage: resource.MustParse("8Gi"),
						},
					},
				},
			}},
		},
	}
	return &sts
}
