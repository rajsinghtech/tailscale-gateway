package proxy

import (
	"context"
	"fmt"

	gatewayv1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ProxyBuilder provides production-grade proxy infrastructure creation
// following official Tailscale k8s-operator ProxyGroup patterns
type ProxyBuilder struct {
	client client.Client
}

// NewProxyBuilder creates a new ProxyBuilder instance
func NewProxyBuilder(client client.Client) *ProxyBuilder {
	return &ProxyBuilder{
		client: client,
	}
}

// PortMapping defines a port mapping for the proxy
type PortMapping struct {
	Name       string
	Port       int32
	Protocol   string // "TCP" or "UDP"
	TargetPort *int32 // Optional different target port
}

// ProxySpec contains all parameters needed for proxy creation
type ProxySpec struct {
	Name           string
	Namespace      string
	Tailnet        string
	ConnectionType string // "ingress", "egress", or "bidirectional"
	Tags           []string
	Ports          []PortMapping
	ProxyConfig    *gatewayv1alpha1.ProxySpec
	OwnerRef       metav1.OwnerReference
	Labels         map[string]string
	Replicas       int32
}

// CreateProxyInfrastructure creates complete proxy infrastructure following ProxyGroup patterns
func (pb *ProxyBuilder) CreateProxyInfrastructure(ctx context.Context, spec *ProxySpec) error {
	logger := log.FromContext(ctx)
	logger.Info("Creating proxy infrastructure", "name", spec.Name, "type", spec.ConnectionType)

	// Create service account with minimal RBAC
	if err := pb.createProxyServiceAccount(ctx, spec); err != nil {
		return fmt.Errorf("failed to create service account: %w", err)
	}

	// Create config secret with tailscaled configuration
	configSecretName := fmt.Sprintf("%s-config", spec.Name)
	if err := pb.createProxyConfigSecret(ctx, spec, configSecretName); err != nil {
		return fmt.Errorf("failed to create config secret: %w", err)
	}

	// Create StatefulSet with production-grade configuration
	if err := pb.createProxyStatefulSet(ctx, spec, configSecretName); err != nil {
		return fmt.Errorf("failed to create StatefulSet: %w", err)
	}

	// Create headless Service for StatefulSet
	if err := pb.createProxyService(ctx, spec); err != nil {
		return fmt.Errorf("failed to create Service: %w", err)
	}

	logger.Info("Proxy infrastructure created successfully", "name", spec.Name)
	return nil
}

// createProxyServiceAccount creates dedicated service account with minimal RBAC
func (pb *ProxyBuilder) createProxyServiceAccount(ctx context.Context, spec *ProxySpec) error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
	}

	if err := pb.createOrUpdate(ctx, serviceAccount); err != nil {
		return err
	}

	// Create Role for state secret access
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch"},
			},
		},
	}

	if err := pb.createOrUpdate(ctx, role); err != nil {
		return err
	}

	// Create RoleBinding
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      spec.Name,
				Namespace: spec.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     spec.Name,
		},
	}

	return pb.createOrUpdate(ctx, roleBinding)
}

// createProxyConfigSecret creates tailscaled configuration secret
func (pb *ProxyBuilder) createProxyConfigSecret(ctx context.Context, spec *ProxySpec, configSecretName string) error {
	// Generate tailscaled configuration following k8s-operator patterns
	config := map[string]interface{}{
		"Version": "alpha0",
		"Locked":  false,
	}

	// Add AdvertiseServices for ingress connections
	if spec.ConnectionType == "ingress" || spec.ConnectionType == "bidirectional" {
		config["AdvertiseServices"] = []string{} // Initialize as empty array
	}

	configData, err := pb.marshalConfig(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configSecretName,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "tailscale-proxy",
				"app.kubernetes.io/instance":   spec.Name,
				"app.kubernetes.io/component":  "config",
				"tailscale.com/managed":        "true",
				"tailscale.com/parent-type":    "proxy",
				"tailscale.com/parent-name":    spec.Name,
			},
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
		Data: map[string][]byte{
			"cap-106.hujson": configData,
		},
	}

	return pb.createOrUpdate(ctx, secret)
}

// createProxyStatefulSet creates StatefulSet with production-grade configuration
func (pb *ProxyBuilder) createProxyStatefulSet(ctx context.Context, spec *ProxySpec, configSecretName string) error {
	// Get container image
	image := "tailscale/tailscale:latest"
	if spec.ProxyConfig != nil && spec.ProxyConfig.Image != "" {
		image = spec.ProxyConfig.Image
	}

	// Convert PortMapping to container ports
	var containerPorts []corev1.ContainerPort
	for _, portMapping := range spec.Ports {
		protocol := corev1.ProtocolTCP
		if portMapping.Protocol == "UDP" {
			protocol = corev1.ProtocolUDP
		}

		containerPorts = append(containerPorts, corev1.ContainerPort{
			Name:          portMapping.Name,
			ContainerPort: portMapping.Port,
			Protocol:      protocol,
		})
	}

	// Generate environment variables following ProxyGroup patterns
	envVars := pb.getProxyEnvironmentVariables(spec, configSecretName)

	// Generate volume mounts
	volumeMounts := pb.getProxyVolumeMounts(spec)

	// Generate volumes
	volumes := pb.getProxyVolumes(spec, configSecretName)

	// Get resource requirements
	resources := pb.getProxyResources(spec.ProxyConfig)

	// Create StatefulSet
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &spec.Replicas,
			ServiceName: spec.Name, // Headless service name
			Selector: &metav1.LabelSelector{
				MatchLabels: spec.Labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: spec.Labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: spec.Name,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						RunAsUser:    &[]int64{65532}[0],
						RunAsGroup:   &[]int64{65532}[0],
						FSGroup:      &[]int64{65532}[0],
					},
					Containers: []corev1.Container{
						{
							Name:  "tailscale",
							Image: image,
							Ports: containerPorts,
							Env:   envVars,
							VolumeMounts: volumeMounts,
							Resources:    resources,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &[]bool{false}[0],
								ReadOnlyRootFilesystem:   &[]bool{true}[0],
								RunAsNonRoot:             &[]bool{true}[0],
								RunAsUser:                &[]int64{65532}[0],
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
									Add:  []corev1.Capability{"NET_ADMIN"},
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/localapi/v0/status",
										Port: intstr.FromInt(41112),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/localapi/v0/status",
										Port: intstr.FromInt(41112),
									},
								},
								InitialDelaySeconds: 20,
								PeriodSeconds:       10,
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	// The current ProxySpec doesn't have scheduling configuration
	// This can be added later when the ProxySpec is extended

	return pb.createOrUpdate(ctx, statefulSet)
}

// createProxyService creates headless Service for the proxy StatefulSet
func (pb *ProxyBuilder) createProxyService(ctx context.Context, spec *ProxySpec) error {
	// Convert PortMapping to service ports
	var servicePorts []corev1.ServicePort
	for _, portMapping := range spec.Ports {
		protocol := corev1.ProtocolTCP
		if portMapping.Protocol == "UDP" {
			protocol = corev1.ProtocolUDP
		}

		targetPort := portMapping.Port
		if portMapping.TargetPort != nil {
			targetPort = *portMapping.TargetPort
		}

		servicePorts = append(servicePorts, corev1.ServicePort{
			Name:       portMapping.Name,
			Port:       portMapping.Port,
			TargetPort: intstr.FromInt(int(targetPort)),
			Protocol:   protocol,
		})
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None", // Headless service
			Selector:  spec.Labels,
			Ports:     servicePorts,
		},
	}

	return pb.createOrUpdate(ctx, service)
}

// Helper functions following exact ProxyGroup patterns

func (pb *ProxyBuilder) getProxyEnvironmentVariables(spec *ProxySpec, configSecretName string) []corev1.EnvVar {
	tailnetName := pb.sanitizeTailnetName(spec.Tailnet)
	
	return []corev1.EnvVar{
		{Name: "TS_KUBE_SECRET", Value: "$(POD_NAME)"},
		{Name: "TS_STATE", Value: "kube:$(POD_NAME)"},
		{Name: "TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR", Value: fmt.Sprintf("/etc/tsconfig/%s", tailnetName)},
		{Name: "TS_USERSPACE", Value: "true"},
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: "POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}},
	}
}

func (pb *ProxyBuilder) getProxyVolumeMounts(spec *ProxySpec) []corev1.VolumeMount {
	tailnetName := pb.sanitizeTailnetName(spec.Tailnet)
	
	return []corev1.VolumeMount{
		{
			Name:      fmt.Sprintf("tailscaledconfig-%s", tailnetName),
			ReadOnly:  true,
			MountPath: fmt.Sprintf("/etc/tsconfig/%s", tailnetName),
		},
	}
}

func (pb *ProxyBuilder) getProxyVolumes(spec *ProxySpec, configSecretName string) []corev1.Volume {
	tailnetName := pb.sanitizeTailnetName(spec.Tailnet)
	
	return []corev1.Volume{
		{
			Name: fmt.Sprintf("tailscaledconfig-%s", tailnetName),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: configSecretName,
				},
			},
		},
	}
}

func (pb *ProxyBuilder) getProxyResources(proxyConfig *gatewayv1alpha1.ProxySpec) corev1.ResourceRequirements {
	// The current ProxySpec doesn't have resource requirements
	// Return default reasonable resources
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}


func (pb *ProxyBuilder) sanitizeTailnetName(tailnet string) string {
	// Simple sanitization for use in Kubernetes resource names
	// Replace dots and other special characters with hyphens
	sanitized := ""
	for _, r := range tailnet {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			sanitized += string(r)
		} else {
			sanitized += "-"
		}
	}
	return sanitized
}

func (pb *ProxyBuilder) marshalConfig(config map[string]interface{}) ([]byte, error) {
	// Simple JSON marshaling for tailscaled configuration
	// In production, this should use proper HuJSON formatting
	result := "{\n"
	first := true
	for k, v := range config {
		if !first {
			result += ",\n"
		}
		first = false
		
		switch val := v.(type) {
		case string:
			result += fmt.Sprintf(`  "%s": "%s"`, k, val)
		case bool:
			result += fmt.Sprintf(`  "%s": %t`, k, val)
		case []string:
			result += fmt.Sprintf(`  "%s": [`, k)
			for i, item := range val {
				if i > 0 {
					result += ", "
				}
				result += fmt.Sprintf(`"%s"`, item)
			}
			result += "]"
		default:
			result += fmt.Sprintf(`  "%s": %v`, k, val)
		}
	}
	result += "\n}"
	
	return []byte(result), nil
}

// createOrUpdate creates or updates a Kubernetes resource
func (pb *ProxyBuilder) createOrUpdate(ctx context.Context, obj client.Object) error {
	err := pb.client.Create(ctx, obj)
	if err != nil {
		// If creation failed, try to update
		if err := pb.client.Update(ctx, obj); err != nil {
			return fmt.Errorf("failed to create or update %T %s/%s: %w", obj, obj.GetNamespace(), obj.GetName(), err)
		}
	}
	return nil
}

// DeleteProxyInfrastructure deletes complete proxy infrastructure
func (pb *ProxyBuilder) DeleteProxyInfrastructure(ctx context.Context, spec *ProxySpec) error {
	logger := log.FromContext(ctx)
	logger.Info("Deleting proxy infrastructure", "name", spec.Name)

	// Delete in reverse order: Service, StatefulSet, ConfigSecret, RBAC, ServiceAccount
	objects := []client.Object{
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: spec.Namespace}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: spec.Namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-config", spec.Name), Namespace: spec.Namespace}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: spec.Namespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: spec.Namespace}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: spec.Namespace}},
	}

	for _, obj := range objects {
		if err := pb.client.Delete(ctx, obj); err != nil {
			logger.Info("Failed to delete resource (may not exist)", "type", fmt.Sprintf("%T", obj), "name", obj.GetName(), "error", err)
		}
	}

	logger.Info("Proxy infrastructure deletion completed", "name", spec.Name)
	return nil
}