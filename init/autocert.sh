#!/bin/bash

#set -x

echo "Welcome to Autocert configuration. Press return to begin."

if [ "$AUTO_START" = false ] ; then
    read ANYKEY
fi

STEPPATH=/home/step

CA_PASSWORD=$(head /dev/urandom | tr -dc A-Za-z0-9 | head -c 32 ; echo '')
AUTOCERT_PASSWORD=$(head /dev/urandom | tr -dc A-Za-z0-9 | head -c 32 ; echo '')

echo -e "\e[1mChecking cluster permissions...\e[0m"

function permission_error {
  # TODO: Figure out the actual service account instead of assuming default.
  echo
  echo -e "\033[0;31mPERMISSION ERROR\033[0m"
  echo "Set permissions by running the following command, then try again:"
  echo -e "\e[1m"
  echo "    kubectl create clusterrolebinding autocert-init-binding \\"
  echo "      --clusterrole cluster-admin \\"
  echo "      --user \"system:serviceaccount:default:default\""
  echo -e "\e[0m"
  echo "Once setup is complete you can remove this binding by running:"
  echo -e "\e[1m"
  echo "    kubectl delete clusterrolebinding autocert-init-binding"
  echo -e "\e[0m"

  exit 1
}

echo -n "Checking for permission to create step namespace: "
kubectl auth can-i create namespaces
if [ $? -ne 0 ]; then
    permission_error "create step namespace"
fi

echo -n "Checking for permission to create configmaps in step namespace: "
kubectl auth can-i create configmaps --namespace step
if [ $? -ne 0 ]; then
    permission_error "create configmaps"
fi

echo -n "Checking for permission to create secrets in step namespace: "
kubectl auth can-i create secrets --namespace step
if [ $? -ne 0 ]; then
    permission_error "create secrets"
fi

echo -n "Checking for permission to create deployments in step namespace: "
kubectl auth can-i create deployments --namespace step
if [ $? -ne 0 ]; then
    permission_error "create deployments"
fi

echo -n "Checking for permission to create services in step namespace: "
kubectl auth can-i create services --namespace step
if [ $? -ne 0 ]; then
    permission_error "create services"
fi

echo -n "Checking for permission to create cluster role: "
kubectl auth can-i create clusterrole
if [ $? -ne 0 ]; then
    permission_error "create cluster roles"
fi

echo -n "Checking for permission to create cluster role binding:"
kubectl auth can-i create clusterrolebinding
if [ $? -ne 0 ]; then
    permission_error "create cluster role bindings"
    exit 1
fi

# Setting this here on purpose, after the above section which explicitly checks
# for and handles exit errors.
set -e

step ca init \
  --name "$CA_NAME" \
  --dns "$CA_DNS" \
  --address "$CA_ADDRESS" \
  --provisioner "$CA_DEFAULT_PROVISIONER" \
  --with-ca-url "$CA_URL" \
  --password-file <(echo "$CA_PASSWORD")

echo
echo -e "\e[1mCreating autocert provisioner...\e[0m"

expect <<EOD
spawn step ca provisioner add autocert --create
expect "Please enter a password to encrypt the provisioner private key? \\\\\\[leave empty and we'll generate one\\\\\\]: "
send "${AUTOCERT_PASSWORD}\n"
expect eof
EOD

echo
echo -e "\e[1mCreating step namespace and preparing environment...\e[0m"

kubectl create namespace step

kubectl -n step create configmap config --from-file $(step path)/config
kubectl -n step create configmap certs --from-file $(step path)/certs
kubectl -n step create configmap secrets --from-file $(step path)/secrets

kubectl -n step create secret generic ca-password --from-literal "password=${CA_PASSWORD}"
kubectl -n step create secret generic autocert-password --from-literal "password=${AUTOCERT_PASSWORD}"

# Deploy CA and wait for rollout to complete
echo
echo -e "\e[1mDeploying certificate authority...\e[0m"

kubectl apply -f https://raw.githubusercontent.com/smallstep/autocert/master/install/01-step-ca.yaml
kubectl -n step rollout status deployment/ca

# Deploy autocert, setup RBAC, and wait for rollout to complete
echo
echo -e "\e[1mDeploying autocert...\e[0m"

kubectl apply -f https://raw.githubusercontent.com/smallstep/autocert/master/install/02-autocert.yaml
kubectl apply -f https://raw.githubusercontent.com/smallstep/autocert/master/install/03-rbac.yaml
kubectl -n step rollout status deployment/autocert

# Some `base64`s wrap lines... no thanks!
CA_BUNDLE=$(cat $(step path)/certs/root_ca.crt | base64 | tr -d '\n')

cat <<EOF | kubectl apply -f -
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: autocert-webhook-config
  labels: {app: autocert}
webhooks:
  - name: autocert.step.sm
    sideEffects: None
    admissionReviewVersions: ["v1beta1"]
    clientConfig:
      service:
        name: autocert
        namespace: step
        path: "/mutate"
      caBundle: $CA_BUNDLE
    rules:
      - operations: ["CREATE"]
        apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["pods"]
    failurePolicy: Ignore
    namespaceSelector:
      matchLabels:
        autocert.step.sm: enabled
EOF

FINGERPRINT=$(step certificate fingerprint $(step path)/certs/root_ca.crt)

echo
echo -e "\e[1mAutocert installed!\e[0m"
echo
echo "Store this information somewhere safe:"
echo "  CA & admin provisioner password: ${CA_PASSWORD}"
echo "  Autocert password: ${AUTOCERT_PASSWORD}"
echo "  CA Fingerprint: ${FINGERPRINT}"
echo

