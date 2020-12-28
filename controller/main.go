package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/smallstep/certificates/ca"
	"github.com/smallstep/certificates/pki"
	"github.com/smallstep/cli/crypto/pemutil"
	"github.com/smallstep/cli/utils"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

const (
	admissionWebhookStatusKey     = "autocert.step.sm/status"
	tlsNameAnnotationKey          = "autocert.step.sm/name"
	tlsDurationAnnotationKey      = "autocert.step.sm/duration"
	sshHostNameAnnotationKey      = "autocert.step.sm/ssh-host-name"
	sshHostDurationAnnotationKey  = "autocert.step.sm/ssh-host-duration"
	volumeMountPath               = "/var/run/autocert.step.sm"
	tokenSecretKey                = "token"
	tokenSecretLabel              = "autocert.step.sm/token"
	tokenLifetime                 = 5 * time.Minute
)

// Config options for the autocert admission controller.
type Config struct {
	Address                         string           `yaml:"address"`
	Service                         string           `yaml:"service"`
	LogFormat                       string           `yaml:"logFormat"`
	CaURL                           string           `yaml:"caUrl"`
	CertLifetime                    string           `yaml:"certLifetime"`
	Bootstrapper                    corev1.Container `yaml:"bootstrapper"`
	Renewer                         corev1.Container `yaml:"renewer"`
	SSHHostCertLifetime             string           `yaml:"sshHostCertLifetime"`
	SSHHostBootstrapper             corev1.Container `yaml:"sshHostBootstrapper"`
	SSHHostRenewer                  corev1.Container `yaml:"sshHostRenewer"`
	CertsVolume                     corev1.Volume    `yaml:"certsVolume"`
	RestrictCertificatesToNamespace bool             `yaml:"restrictCertificatesToNamespace"`
	ClusterDomain                   string           `yaml:"clusterDomain"`
	RootCAPath                      string           `yaml:"rootCAPath"`
	ProvisionerPasswordPath         string           `yaml:"provisionerPasswordPath"`
}

type IdentityRequest interface {
	getSubject() string
	mkBootstrapper(config *Config, fingerprint, podName, namespace string, provisioner *ca.Provisioner) (corev1.Container, error)
	mkRenewer(config *Config, podName, namespace string) corev1.Container
}

type TLSIdentityRequest struct {
	commonName string
	duration   string
}

type SSHIdentityRequest struct {
	hostCert bool
	keyID    string
	duration string
}

// GetAddress returns the address set in the configuration, defaults to ":4443"
// if it's not specified.
func (c Config) GetAddress() string {
	if c.Address != "" {
		return c.Address
	}

	return ":4443"
}

// GetServiceName returns the service name set in the configuration, defaults to
// "autocert" if it's not specified.
func (c Config) GetServiceName() string {
	if c.Service != "" {
		return c.Service
	}

	return "autocert"
}

// GetClusterDomain returns the Kubernetes cluster domain, defaults to
// "cluster.local" if not specified in the configuration.
func (c Config) GetClusterDomain() string {
	if c.ClusterDomain != "" {
		return c.ClusterDomain
	}

	return "cluster.local"
}

// GetRootCAPath returns the root CA path in the configuration, defaults to
// "STEPPATH/certs/root_ca.crt" if it's not specified.
func (c Config) GetRootCAPath() string {
	if c.RootCAPath != "" {
		return c.RootCAPath
	}

	return pki.GetRootCAPath()
}

// GetProvisionerPasswordPath returns the path to the provisioner password,
// defaults to "/home/step/password/password" if not specified in the
// configuration.
func (c Config) GetProvisionerPasswordPath() string {
	if c.ProvisionerPasswordPath != "" {
		return c.ProvisionerPasswordPath
	}

	return "/home/step/password/password"
}

// getSubject returns the identity being requested, so it can be validated
func (req TLSIdentityRequest) getSubject() string {
	return req.commonName
}

// mkBootstrapper generates a bootstrap container based on the template defined in Config. It
// generates a new bootstrap token and mounts it, along with other required configuration, as
// environment variables in the returned bootstrap container.
func (req TLSIdentityRequest) mkBootstrapper(config *Config, fingerprint, podName, namespace string, provisioner *ca.Provisioner) (corev1.Container, error) {
	b := config.Bootstrapper

	token, err := provisioner.Token(req.commonName)
	if err != nil {
		return b, errors.Wrap(err, "tls token generation")
	}

	secretName, err := createTokenSecret(req.commonName+"-", namespace, token)
	if err != nil {
		return b, errors.Wrap(err, "create tls token secret")
	}
	log.Infof("TLS secret name is: %s", secretName)

	b.Env = append(b.Env, corev1.EnvVar{
		Name: "STEP_TOKEN",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretName,
				},
				Key: tokenSecretKey,
			},
		},
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "COMMON_NAME",
		Value: req.commonName,
	})
	if req.duration != "" {
		b.Env = append(b.Env, corev1.EnvVar{
			Name:  "STEP_NOT_AFTER",
			Value: req.duration,
		})
	} else {
		b.Env = append(b.Env, corev1.EnvVar{
			Name:  "STEP_NOT_AFTER",
			Value: config.CertLifetime,
		})
	}

	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "STEP_CA_URL",
		Value: config.CaURL,
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "STEP_FINGERPRINT",
		Value: fingerprint,
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "POD_NAME",
		Value: podName,
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "NAMESPACE",
		Value: namespace,
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "CLUSTER_DOMAIN",
		Value: config.ClusterDomain,
	})

	return b, nil
}

// mkRenewer generates a new renewer based on the template provided in Config.
func (req TLSIdentityRequest) mkRenewer(config *Config, podName, namespace string) corev1.Container {
	r := config.Renewer
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "STEP_CA_URL",
		Value: config.CaURL,
	})
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "COMMON_NAME",
		Value: req.commonName,
	})
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "POD_NAME",
		Value: podName,
	})
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "NAMESPACE",
		Value: namespace,
	})
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "CLUSTER_DOMAIN",
		Value: config.ClusterDomain,
	})
	return r
}

// getSubject returns the identity being requested, so it can be validated
func (req SSHIdentityRequest) getSubject() string {
	return req.keyID
}

// mkBootstrapper generates a bootstrap container based on the template defined in Config. It
// generates a new bootstrap token and mounts it, along with other required configuration, as
// environment variables in the returned bootstrap container.
func (req SSHIdentityRequest) mkBootstrapper(config *Config, fingerprint, podName, namespace string, provisioner *ca.Provisioner) (corev1.Container, error) {
	var b corev1.Container
	var certType string

	if req.hostCert {
		b = config.SSHHostBootstrapper
		certType = "host"
	} else {
		return b, errors.New("Client SSH certificates not supported")
	}

	token, err := provisioner.SSHToken(certType, req.keyID, []string{})
	if err != nil {
		return b, errors.Wrap(err, "ssh token generation")
	}

	secretName, err := createTokenSecret(req.keyID+"-", namespace, token)
	if err != nil {
		return b, errors.Wrap(err, "create ssh token secret")
	}
	log.Infof("SSH secret name is: %s", secretName)

	b.Env = append(b.Env, corev1.EnvVar{
		Name: "STEP_TOKEN",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretName,
				},
				Key: tokenSecretKey,
			},
		},
	})
	if req.hostCert {
		b.Env = append(b.Env, corev1.EnvVar{
			Name:  "STEP_HOST",
			Value: "true",
		})
	}
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "KEY_ID",
		Value: req.keyID,
	})
	if req.duration != "" {
		b.Env = append(b.Env, corev1.EnvVar{
			Name:  "STEP_NOT_AFTER",
			Value: req.duration,
		})
	} else {
	  var lifetime string
	  if req.hostCert {
	    lifetime = config.SSHHostCertLifetime
	  } else {
			return b, errors.New("Client SSH certificates not supported")
	  }
		b.Env = append(b.Env, corev1.EnvVar{
			Name:  "STEP_NOT_AFTER",
			Value: lifetime,
		})
	}

	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "STEP_CA_URL",
		Value: config.CaURL,
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "STEP_FINGERPRINT",
		Value: fingerprint,
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "POD_NAME",
		Value: podName,
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "NAMESPACE",
		Value: namespace,
	})
	b.Env = append(b.Env, corev1.EnvVar{
		Name:  "CLUSTER_DOMAIN",
		Value: config.ClusterDomain,
	})

	return b, nil
}

// mkRenewer generates a new renewer based on the template provided in Config.
func (req SSHIdentityRequest) mkRenewer(config *Config, podName, namespace string) corev1.Container {
	r := config.SSHHostRenewer
	if req.hostCert {
		r.Env = append(r.Env, corev1.EnvVar{
			Name:  "STEP_HOST",
			Value: "true",
		})
	}
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "STEP_CA_URL",
		Value: config.CaURL,
	})
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "POD_NAME",
		Value: podName,
	})
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "NAMESPACE",
		Value: namespace,
	})
	r.Env = append(r.Env, corev1.EnvVar{
		Name:  "CLUSTER_DOMAIN",
		Value: config.ClusterDomain,
	})
	return r
}

// PatchOperation represents a RFC6902 JSONPatch Operation
type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// RFC6901 JSONPath Escaping -- https://tools.ietf.org/html/rfc6901
func escapeJSONPath(path string) string {
	// Replace`~` with `~0` then `/` with `~1`. Note that the order
	// matters otherwise we'll turn a `/` into a `~/`.
	path = strings.Replace(path, "~", "~0", -1)
	path = strings.Replace(path, "/", "~1", -1)
	return path
}

func loadConfig(file string) (*Config, error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// createTokenSecret generates a kubernetes Secret object containing a bootstrap token
// in the specified namespace. The secret name is randomly generated with a given prefix.
// A goroutine is scheduled to cleanup the secret after the token expires. The secret
// is also labelled for easy identification and manual cleanup.
func createTokenSecret(prefix, namespace, token string) (string, error) {
	secret := corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix,
			Namespace:    namespace,
			Labels: map[string]string{
				tokenSecretLabel: "true",
			},
		},
		StringData: map[string]string{
			tokenSecretKey: token,
		},
		Type: corev1.SecretTypeOpaque,
	}

	client, err := NewInClusterK8sClient()
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(secret)
	if err != nil {
		return "", err
	}
	log.WithField("secret", string(body)).Debug("Creating secret")

	req, err := client.PostRequest(fmt.Sprintf("api/v1/namespaces/%s/secrets", namespace), string(body), "application/json")
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Errorf("Secret creation error. Response: %v", resp)
		return "", errors.Wrap(err, "secret creation")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Errorf("Secret creation error (!2XX). Response: %v", resp)
		var rbody []byte
		if resp.Body != nil {
			if data, err := ioutil.ReadAll(resp.Body); err == nil {
				rbody = data
			}
		}
		log.Error("Error body: ", string(rbody))
		return "", errors.New("Not 200")
	}

	var rbody []byte
	if resp.Body != nil {
		if data, err := ioutil.ReadAll(resp.Body); err == nil {
			rbody = data
		}
	}
	if len(rbody) == 0 {
		return "", errors.New("Empty response body")
	}

	var created *corev1.Secret
	if err := json.Unmarshal(rbody, &created); err != nil {
		return "", errors.Wrap(err, "Error unmarshalling secret response")
	}

	// Clean up after ourselves by deleting the Secret after the bootstrap
	// token expires. This is best effort -- obviously we'll miss some stuff
	// if this process goes away -- but the secrets are also labelled so
	// it's also easy to clean them up in bulk using kubectl if we miss any.
	go func() {
		time.Sleep(tokenLifetime)
		req, err := client.DeleteRequest(fmt.Sprintf("api/v1/namespaces/%s/secrets/%s", namespace, created.Name))
		ctxLog := log.WithFields(log.Fields{
			"name":      created.Name,
			"namespace": namespace,
		})
		if err != nil {
			ctxLog.WithField("error", err).Error("Error deleting expired bootstrap token secret")
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			ctxLog.WithField("error", err).Error("Error deleting expired bootstrap token secret")
			return
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			ctxLog.WithFields(log.Fields{
				"status":     resp.Status,
				"statusCode": resp.StatusCode,
			}).Error("Error deleting expired bootstrap token secret")
			return
		}
		ctxLog.Info("Deleted expired bootstrap token secret")
	}()

	return created.Name, err
}

func addContainers(existing, new []corev1.Container, path string) (ops []PatchOperation) {
	if len(existing) == 0 {
		return []PatchOperation{
			{
				Op:    "add",
				Path:  path,
				Value: new,
			},
		}
	}

	for _, add := range new {
		ops = append(ops, PatchOperation{
			Op:    "add",
			Path:  path + "/-",
			Value: add,
		})
	}
	return ops
}

func addVolumes(existing, new []corev1.Volume, path string) (ops []PatchOperation) {
	if len(existing) == 0 {
		return []PatchOperation{
			{
				Op:    "add",
				Path:  path,
				Value: new,
			},
		}
	}

	for _, add := range new {
		ops = append(ops, PatchOperation{
			Op:    "add",
			Path:  path + "/-",
			Value: add,
		})
	}
	return ops
}

func addCertsVolumeMount(volumeName string, containers []corev1.Container) (ops []PatchOperation) {
	volumeMount := corev1.VolumeMount{
		Name:      volumeName,
		MountPath: volumeMountPath,
		ReadOnly:  true,
	}
	for i, container := range containers {
		if len(container.VolumeMounts) == 0 {
			ops = append(ops, PatchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%v/volumeMounts", i),
				Value: []corev1.VolumeMount{volumeMount},
			})
		} else {
			ops = append(ops, PatchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%v/volumeMounts/-", i),
				Value: volumeMount,
			})
		}
	}
	return ops
}

func addAnnotations(existing, new map[string]string) (ops []PatchOperation) {
	if len(existing) == 0 {
		return []PatchOperation{
			{
				Op:    "add",
				Path:  "/metadata/annotations",
				Value: new,
			},
		}
	}
	for k, v := range new {
		if existing[k] == "" {
			ops = append(ops, PatchOperation{
				Op:    "add",
				Path:  "/metadata/annotations/" + escapeJSONPath(k),
				Value: v,
			})
		} else {
			ops = append(ops, PatchOperation{
				Op:    "replace",
				Path:  "/metadata/annotations/" + escapeJSONPath(k),
				Value: v,
			})
		}
	}
	return ops
}

// patch produces a list of patches to apply to a pod to inject a certificate. In particular,
// we patch the pod in order to:
//  - Mount the `certs` volume in existing containers defined in the pod
//  - Add the autocert-renewer as a container (a sidecar)
//  - Add the autocert-bootstrapper as an initContainer
//  - Add the `certs` volume definition
//  - Annotate the pod to indicate that it's been processed by this controller
// The result is a list of serialized JSONPatch objects (or an error).
func patch(pod *corev1.Pod, namespace string, config *Config, provisioner *ca.Provisioner, idReqs []IdentityRequest) ([]byte, error) {
	var ops []PatchOperation

	name := pod.ObjectMeta.GetName()
	if name == "" {
		name = pod.ObjectMeta.GetGenerateName()
	}

	// Generate CA fingerprint
	crt, err := pemutil.ReadCertificate(config.GetRootCAPath())
	if err != nil {
		return nil, errors.Wrap(err, "CA fingerprint")
	}
	sum := sha256.Sum256(crt.Raw)
	fingerprint := strings.ToLower(hex.EncodeToString(sum[:]))

	bootstrappers := make([]corev1.Container, len(idReqs))
	renewers := make([]corev1.Container, len(idReqs))

	for i, ireq := range idReqs {
		bootstrapper, err := ireq.mkBootstrapper(config, fingerprint, name, namespace, provisioner)
		if err != nil {
			return nil, err
		}
	  bootstrappers[i] = bootstrapper
		renewers[i] = ireq.mkRenewer(config, name, namespace)
	}

	ops = append(ops, addContainers(pod.Spec.InitContainers, bootstrappers, "/spec/initContainers")...)
	ops = append(ops, addContainers(pod.Spec.Containers, renewers, "/spec/containers")...)
	ops = append(ops, addCertsVolumeMount(config.CertsVolume.Name, pod.Spec.Containers)...)
	ops = append(ops, addVolumes(pod.Spec.Volumes, []corev1.Volume{config.CertsVolume}, "/spec/volumes")...)
	ops = append(ops, addAnnotations(pod.Annotations, map[string]string{admissionWebhookStatusKey: "injected"})...)

	return json.Marshal(ops)
}

// shouldMutate checks whether a pod is subject to mutation by this admission controller. A pod
// is subject to mutation if it's annotated with the `admissionWebhookAnnotationKey` and if it
// has not already been processed (indicated by `admissionWebhookStatusKey` set to `injected`).
// If the pod requests a certificate with a subject matching a namespace other than its own
// and restrictToNamespace is true, then shouldMutate will return a validation error
// that should be returned to the client.
func shouldMutate(req IdentityRequest, namespace string, clusterDomain string, restrictToNamespace bool) (bool, error) {
	subject := strings.Trim(req.getSubject(), ".")

	if subject == "" {
		return false, nil
	}

	if !restrictToNamespace {
		return true, nil
	}

	err := fmt.Errorf("subject \"%s\" matches a namespace other than \"%s\" and is not permitted. This check can be disabled by setting restrictCertificatesToNamespace to false in the autocert-config ConfigMap", subject, namespace)

	if strings.HasSuffix(subject, ".svc") && !strings.HasSuffix(subject, fmt.Sprintf(".%s.svc", namespace)) {
		return false, err
	}

	if strings.HasSuffix(subject, fmt.Sprintf(".svc.%s", clusterDomain)) && !strings.HasSuffix(subject, fmt.Sprintf(".%s.svc.%s", namespace, clusterDomain)) {
		return false, err
	}

	return true, nil
}

// mutate takes an `AdmissionReview`, determines whether it is subject to mutation, and returns
// an appropriate `AdmissionResponse` including patches or any errors that occurred.
func mutate(review *v1beta1.AdmissionReview, config *Config, provisioner *ca.Provisioner) *v1beta1.AdmissionResponse {
	ctxLog := log.WithField("uid", review.Request.UID)

	request := review.Request
	var pod corev1.Pod
	if err := json.Unmarshal(request.Object.Raw, &pod); err != nil {
		ctxLog.WithField("error", err).Error("Error unmarshaling pod")
		return &v1beta1.AdmissionResponse{
			Allowed: false,
			UID:     request.UID,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	ctxLog = ctxLog.WithFields(log.Fields{
		"kind":         request.Kind,
		"operation":    request.Operation,
		"name":         pod.Name,
		"generateName": pod.GenerateName,
		"namespace":    request.Namespace,
		"user":         request.UserInfo,
	})

	annotations := pod.ObjectMeta.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	// Don't mutate if it's been mutated already
	if annotations[admissionWebhookStatusKey] == "injected" {
		return &v1beta1.AdmissionResponse{
			Allowed: true,
			UID:     request.UID,
		}
	}

	tlsReq := TLSIdentityRequest{
		commonName: annotations[tlsNameAnnotationKey],
		duration: annotations[tlsDurationAnnotationKey],
	}

	sshHostReq := SSHIdentityRequest{
		hostCert: true,
		keyID: annotations[sshHostNameAnnotationKey],
		duration: annotations[sshHostDurationAnnotationKey],
	}

	potentialIdReqs := []IdentityRequest{tlsReq, sshHostReq}
	idReqs := []IdentityRequest{}

	for _, ireq := range potentialIdReqs {
		mutationAllowed, validationErr := shouldMutate(ireq, request.Namespace, config.GetClusterDomain(), config.RestrictCertificatesToNamespace)

		if validationErr != nil {
			ctxLog.WithField("error", validationErr).Info("Validation error")
			return &v1beta1.AdmissionResponse{
				Allowed: false,
				UID:     request.UID,
				Result: &metav1.Status{
					Message: validationErr.Error(),
				},
			}
		}

		if mutationAllowed {
			idReqs = append(idReqs, ireq)
		}
	}

	if len(idReqs) == 0 {
		ctxLog.WithField("annotations", pod.Annotations).Info("Skipping mutation")
		return &v1beta1.AdmissionResponse{
			Allowed: true,
			UID:     request.UID,
		}
	}

	patchBytes, err := patch(&pod, request.Namespace, config, provisioner, idReqs)
	if err != nil {
		ctxLog.WithField("error", err).Error("Error generating patch")
		return &v1beta1.AdmissionResponse{
			Allowed: false,
			UID:     request.UID,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	ctxLog.WithField("patch", string(patchBytes)).Info("Generated patch")
	return &v1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		UID:     request.UID,
		PatchType: func() *v1beta1.PatchType {
			pt := v1beta1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

func main() {
	if len(os.Args) != 2 {
		log.Errorf("Usage: %s <config>\n", os.Args[0])
		os.Exit(1)
	}

	config, err := loadConfig(os.Args[1])
	if err != nil {
		panic(err)
	}

	log.SetOutput(os.Stdout)
	if config.LogFormat == "json" {
		log.SetFormatter(&log.JSONFormatter{})
	}
	if config.LogFormat == "text" {
		log.SetFormatter(&log.TextFormatter{})
	}

	log.WithFields(log.Fields{
		"config": config,
	}).Info("Loaded config")

	provisionerName := os.Getenv("PROVISIONER_NAME")
	provisionerKid := os.Getenv("PROVISIONER_KID")
	log.WithFields(log.Fields{
		"provisionerName": provisionerName,
		"provisionerKid":  provisionerKid,
	}).Info("Loaded provisioner configuration")

	password, err := utils.ReadPasswordFromFile(config.GetProvisionerPasswordPath())
	if err != nil {
		panic(err)
	}

	provisioner, err := ca.NewProvisioner(
		provisionerName, provisionerKid, config.CaURL, password,
		ca.WithRootFile(config.GetRootCAPath()))
	if err != nil {
		log.Errorf("Error loading provisioner: %v", err)
		os.Exit(1)
	}
	log.WithFields(log.Fields{
		"name": provisioner.Name(),
		"kid":  provisioner.Kid(),
	}).Info("Loaded provisioner")

	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		log.Errorf("$NAMESPACE not set")
		os.Exit(1)
	}

	name := fmt.Sprintf("%s.%s.svc", config.GetServiceName(), namespace)
	token, err := provisioner.Token(name)
	if err != nil {
		log.WithField("error", err).Errorf("Error generating bootstrap token during controller startup")
		os.Exit(1)
	}
	log.WithField("name", name).Infof("Generated bootstrap token for controller")

	// make sure to cancel the renew goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := ca.BootstrapServer(ctx, token, &http.Server{
		Addr: config.GetAddress(),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				log.Info("/healthz")
				w.WriteHeader(http.StatusOK)
				fmt.Fprintln(w, "ok")
				return
			}

			if r.URL.Path != "/mutate" {
				log.WithField("path", r.URL.Path).Error("Bad Request: 404 Not Found")
				http.NotFound(w, r)
				return
			}

			var body []byte
			if r.Body != nil {
				if data, err := ioutil.ReadAll(r.Body); err == nil {
					body = data
				}
			}
			if len(body) == 0 {
				log.Error("Bad Request: 400 (Empty Body)")
				http.Error(w, "Bad Request (Empty Body)", http.StatusBadRequest)
				return
			}

			contentType := r.Header.Get("Content-Type")
			if contentType != "application/json" {
				log.WithField("Content-Type", contentType).Error("Bad Request: 415 (Unsupported Media Type)")
				http.Error(w, fmt.Sprintf("Bad Request: 415 Unsupported Media Type (Expected Content-Type 'application/json' but got '%s')", contentType), http.StatusUnsupportedMediaType)
				return
			}

			var response *v1beta1.AdmissionResponse
			review := v1beta1.AdmissionReview{}
			if _, _, err := deserializer.Decode(body, nil, &review); err != nil {
				log.WithFields(log.Fields{
					"body":  body,
					"error": err,
				}).Error("Can't decode body")
				response = &v1beta1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Message: err.Error(),
					},
				}
			} else {
				response = mutate(&review, config, provisioner)
			}

			resp, err := json.Marshal(v1beta1.AdmissionReview{
				Response: response,
			})
			if err != nil {
				log.WithFields(log.Fields{
					"uid":   review.Request.UID,
					"error": err,
				}).Info("Marshal error")
				http.Error(w, fmt.Sprintf("Marshal Error: %v", err), http.StatusInternalServerError)
			} else {
				log.WithFields(log.Fields{
					"uid":      review.Request.UID,
					"response": string(resp),
				}).Info("Returning review")
				if _, err := w.Write(resp); err != nil {
					log.WithFields(log.Fields{
						"uid":   review.Request.UID,
						"error": err,
					}).Info("Write error")
				}
			}
		}),
	}, ca.VerifyClientCertIfGiven())
	if err != nil {
		panic(err)
	}

	log.Info("Listening on", config.GetAddress(), "...")
	if err := srv.ListenAndServeTLS("", ""); err != nil {
		panic(err)
	}
}
