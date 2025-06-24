// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	crdPlaceholderStart = "# PLACEHOLDER_CRDS_START"
	crdPlaceholderEnd   = "# PLACEHOLDER_CRDS_END"
)

func main() {
	var (
		crdDir       = flag.String("crd-dir", "config/crd/bases", "Directory containing CRD YAML files")
		chartDir     = flag.String("chart-dir", "cmd/main/deploy/chart", "Helm chart directory")
		manifestDir  = flag.String("manifest-dir", "cmd/main/deploy/manifests", "Output directory for static manifests")
		crdOutputDir = flag.String("crd-output-dir", "cmd/main/deploy/crds", "Output directory for CRD files")
	)
	flag.Parse()

	// Step 1: Copy CRDs to the deployment directory
	if err := copyCRDs(*crdDir, *crdOutputDir); err != nil {
		log.Fatalf("Failed to copy CRDs: %v", err)
	}

	// Step 2: Insert CRDs into Helm chart
	if err := insertCRDsIntoHelm(*crdDir, *chartDir); err != nil {
		log.Fatalf("Failed to insert CRDs into Helm chart: %v", err)
	}

	// Step 3: Generate static manifests from Helm chart
	if err := generateStaticManifests(*chartDir, *manifestDir, *crdDir); err != nil {
		log.Fatalf("Failed to generate static manifests: %v", err)
	}

	fmt.Println("Successfully generated deployment artifacts")
}

func copyCRDs(srcDir, dstDir string) error {
	// Create destination directory
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create CRD output directory: %w", err)
	}

	// Read CRD files
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("failed to read CRD directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		// Copy file
		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to copy CRD %s: %w", entry.Name(), err)
		}
	}

	return nil
}

func insertCRDsIntoHelm(crdDir, chartDir string) error {
	// Read all CRDs
	crdContent, err := readAllCRDs(crdDir)
	if err != nil {
		return fmt.Errorf("failed to read CRDs: %w", err)
	}

	// Read the CRDs template
	templatePath := filepath.Join(chartDir, "templates", "crds.yaml")
	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read CRDs template: %w", err)
	}

	// Replace placeholder with actual CRDs
	updatedContent := replacePlaceholder(string(templateContent), crdContent)

	// Write updated template
	if err := os.WriteFile(templatePath, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated CRDs template: %w", err)
	}

	return nil
}

func readAllCRDs(crdDir string) (string, error) {
	var buf bytes.Buffer

	entries, err := os.ReadDir(crdDir)
	if err != nil {
		return "", err
	}

	crdCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(crdDir, entry.Name()))
		if err != nil {
			return "", fmt.Errorf("failed to read CRD %s: %w", entry.Name(), err)
		}

		// For the first CRD, don't add a separator (the template has "--- " already)
		// For subsequent CRDs, add separator
		if crdCount > 0 {
			buf.WriteString("\n---\n")
		}
		crdCount++

		buf.Write(content)
	}

	return buf.String(), nil
}

func replacePlaceholder(template, content string) string {
	startIdx := strings.Index(template, crdPlaceholderStart)
	endIdx := strings.Index(template, crdPlaceholderEnd)

	if startIdx == -1 || endIdx == -1 {
		log.Printf("Warning: CRD placeholders not found in template")
		return template
	}

	// Find the newline after the placeholder start line (which ends with " ---")
	placeholderLineEnd := strings.Index(template[startIdx:], "\n")
	if placeholderLineEnd == -1 {
		log.Printf("Warning: Could not find end of PLACEHOLDER_CRDS_START line")
		return template
	}
	
	// Position right after the newline of the placeholder start line
	afterPlaceholderLine := startIdx + placeholderLineEnd + 1

	// Keep the placeholder markers and replace everything between them
	indentedContent := indentContent(content, 0)

	before := template[:afterPlaceholderLine]
	after := template[endIdx:]

	return before + indentedContent + "\n" + after
}

func indentContent(content string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

func generateStaticManifests(chartDir, manifestDir, crdDir string) error {
	// Create manifest directory
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		return fmt.Errorf("failed to create manifest directory: %w", err)
	}

	// Create a combined manifest file
	outputPath := filepath.Join(manifestDir, "operator.yaml")
	output, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer output.Close()

	// First, add namespace
	namespace := `apiVersion: v1
kind: Namespace
metadata:
  name: tailscale-gateway-system
---
`
	if _, err := output.WriteString(namespace); err != nil {
		return err
	}

	// Add CRDs
	crdContent, err := readAllCRDs(crdDir)
	if err != nil {
		return err
	}
	if _, err := output.WriteString(crdContent); err != nil {
		return err
	}
	if _, err := output.WriteString("\n---\n"); err != nil {
		return err
	}

	// Add pre-existing templates from manifests/templates if they exist
	templatesDir := filepath.Join(manifestDir, "templates")
	if _, err := os.Stat(templatesDir); err == nil {
		templates, err := os.ReadDir(templatesDir)
		if err != nil {
			return fmt.Errorf("failed to read templates directory: %w", err)
		}

		for _, tmpl := range templates {
			if tmpl.IsDir() || !strings.HasSuffix(tmpl.Name(), ".yaml") {
				continue
			}

			content, err := os.ReadFile(filepath.Join(templatesDir, tmpl.Name()))
			if err != nil {
				return fmt.Errorf("failed to read template %s: %w", tmpl.Name(), err)
			}

			if _, err := output.Write(content); err != nil {
				return err
			}
			if _, err := output.WriteString("\n---\n"); err != nil {
				return err
			}
		}
	}

	// Generate basic manifests from Helm templates (simplified version)
	// In a real implementation, you might want to use Helm SDK to render templates
	// For now, we'll create a basic static manifest
	if err := generateBasicManifests(output); err != nil {
		return fmt.Errorf("failed to generate basic manifests: %w", err)
	}

	return nil
}

func generateBasicManifests(w io.Writer) error {
	// This is a simplified version. In production, you'd want to properly
	// render Helm templates or maintain separate static templates
	manifests := []string{
		// ServiceAccount
		`apiVersion: v1
kind: ServiceAccount
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-gateway-system`,
		// ClusterRole
		`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: tailscale-gateway-operator-role
rules:
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
- apiGroups: [""]
  resources: ["secrets", "services", "configmaps"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["apps"]
  resources: ["deployments", "statefulsets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["gateway.tailscale.com"]
  resources: ["tailscaletailnets", "tailscalegateways", "tailscaleroutepolicies"]
  verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
- apiGroups: ["gateway.tailscale.com"]
  resources: ["tailscaletailnets/finalizers", "tailscalegateways/finalizers", "tailscaleroutepolicies/finalizers"]
  verbs: ["update"]
- apiGroups: ["gateway.tailscale.com"]
  resources: ["tailscaletailnets/status", "tailscalegateways/status", "tailscaleroutepolicies/status"]
  verbs: ["get", "patch", "update"]
- apiGroups: ["gateway.networking.k8s.io"]
  resources: ["gateways", "httproutes", "grpcroutes", "tcproutes", "tlsroutes", "udproutes"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["gateway.networking.k8s.io"]
  resources: ["gateways/status", "httproutes/status", "grpcroutes/status", "tcproutes/status", "tlsroutes/status", "udproutes/status"]
  verbs: ["get", "patch", "update"]
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]`,
		// ClusterRoleBinding
		`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: tailscale-gateway-operator-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: tailscale-gateway-operator-role
subjects:
- kind: ServiceAccount
  name: tailscale-gateway-operator
  namespace: tailscale-gateway-system`,
		// Deployment
		`apiVersion: apps/v1
kind: Deployment
metadata:
  name: tailscale-gateway-operator
  namespace: tailscale-gateway-system
  labels:
    app.kubernetes.io/name: tailscale-gateway-operator
    app.kubernetes.io/component: operator
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: tailscale-gateway-operator
      app.kubernetes.io/component: operator
  template:
    metadata:
      labels:
        app.kubernetes.io/name: tailscale-gateway-operator
        app.kubernetes.io/component: operator
    spec:
      serviceAccountName: tailscale-gateway-operator
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
      containers:
      - name: manager
        image: ghcr.io/rajsinghtech/tailscale-gateway-operator:v0.1.0
        imagePullPolicy: IfNotPresent
        args:
        - --health-probe-bind-address=:8081
        - --metrics-bind-address=:8080
        - --leader-elect
        - --log-level=info
        env:
        - name: OPERATOR_NAMESPACE
          value: tailscale-gateway-system
        - name: PROXY_IMAGE
          value: tailscale/tailscale:stable
        ports:
        - name: metrics
          containerPort: 8080
          protocol: TCP
        - name: health
          containerPort: 8081
          protocol: TCP
        livenessProbe:
          httpGet:
            path: /healthz
            port: health
          initialDelaySeconds: 15
          periodSeconds: 20
        readinessProbe:
          httpGet:
            path: /readyz
            port: health
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          limits:
            cpu: 500m
            memory: 128Mi
          requests:
            cpu: 10m
            memory: 64Mi
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop:
            - ALL
        volumeMounts:
        - mountPath: /tmp
          name: temp
      volumes:
      - name: temp
        emptyDir: {}`,
	}

	for i, manifest := range manifests {
		if i > 0 {
			if _, err := w.Write([]byte("\n---\n")); err != nil {
				return err
			}
		}
		if _, err := w.Write([]byte(manifest)); err != nil {
			return err
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}
