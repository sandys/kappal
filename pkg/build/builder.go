package build

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	BuilderNamespace  = "kappal-system"
	BuilderName       = "kappal-builder"
	BuildKitImage     = "moby/buildkit:v0.12.0"
)

// Builder manages the in-cluster BuildKit deployment
type Builder struct {
	clientset *kubernetes.Clientset
}

// NewBuilder creates a new builder manager
func NewBuilder(clientset *kubernetes.Clientset) *Builder {
	return &Builder{clientset: clientset}
}

// EnsureBuilder ensures the BuildKit deployment exists in the cluster
func (b *Builder) EnsureBuilder(ctx context.Context) error {
	// Ensure namespace exists
	if err := b.ensureNamespace(ctx); err != nil {
		return err
	}

	// Check if deployment exists
	_, err := b.clientset.AppsV1().Deployments(BuilderNamespace).Get(ctx, BuilderName, metav1.GetOptions{})
	if err == nil {
		return nil // Already exists
	}

	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check builder: %w", err)
	}

	// Create deployment
	deployment := b.createDeployment()
	_, err = b.clientset.AppsV1().Deployments(BuilderNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create builder: %w", err)
	}

	return nil
}

func (b *Builder) ensureNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: BuilderNamespace,
			Labels: map[string]string{
				"kappal.io/system": "true",
			},
		},
	}

	_, err := b.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	return nil
}

func (b *Builder) createDeployment() *appsv1.Deployment {
	privileged := true
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      BuilderName,
			Namespace: BuilderNamespace,
			Labels: map[string]string{
				"kappal.io/component": "builder",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kappal.io/component": "builder",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kappal.io/component": "builder",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "buildkitd",
							Image: BuildKitImage,
							Args: []string{
								"--oci-worker=false",
								"--containerd-worker=true",
								"--containerd-worker-addr=/run/k3s/containerd/containerd.sock",
								"--containerd-worker-namespace=k8s.io",
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "k3s-socket",
									MountPath: "/run/k3s/containerd/containerd.sock",
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "buildkit",
									ContainerPort: 1234,
									Protocol:      corev1.ProtocolTCP,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "k3s-socket",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/run/k3s/containerd/containerd.sock",
									Type: hostPathTypePtr(corev1.HostPathSocket),
								},
							},
						},
					},
				},
			},
		},
	}
}

func hostPathTypePtr(t corev1.HostPathType) *corev1.HostPathType {
	return &t
}

// RemoveBuilder removes the BuildKit deployment
func (b *Builder) RemoveBuilder(ctx context.Context) error {
	err := b.clientset.AppsV1().Deployments(BuilderNamespace).Delete(ctx, BuilderName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete builder: %w", err)
	}
	return nil
}
