# Autocert Configuration

## Configurable Mount Path

By default, autocert mounts certificates at `/var/run/autocert.step.sm/`. You can now customize this path using annotations.

### Usage

Add the `autocert.step.sm/mount-path` annotation to your pod:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    metadata:
      annotations:
        autocert.step.sm/name: my-app.default.svc.cluster.local
        autocert.step.sm/mount-path: /custom/cert/path
    spec:
      containers:
      - name: app
        image: my-app:latest
```
## Default Behavior

Without annotation: /var/run/autocert.step.sm/
With annotation: Your specified custom path

## File Structure
Certificates will be available at:

<mount-path>/site.crt
<mount-path>/site.key
<mount-path>/root.crt

### Important: Custom Controller Image Required
Note: This configurable mount path feature requires an updated controller image that is not yet available in the official repository. To use this feature, you need to:
## 1 Build Custom Controller Image
Since the original YAML files haven't been updated with the new images, you'll need to build and use custom Docker images with the updated code:
```bash
docker build -t your-registry/autocert-controller:custom -f controller/Dockerfile .
```
## 2 Load image to cluster
```bash
#for minikube:
minikube image load autocert-controller:custom

#for kind
kind load docker-image autocert-controller:custom --name <your cluster name>
```
## 3 Update Controller Deployment
Update the autocert controller deployment to use your custom image:

```bash
#restart deployment(if needed)
kubectl rollout restart deployment/<your-deployment-name>
#For local clusters (minikube, kind, Docker Desktop):
kubectl patch deployment autocert -n step -p '{"spec":{"template":{"spec":{"containers":[{"name":"autocert","image":"autocert-controller:custom","imagePullPolicy":"Never"}]}}}}'

#For remote clusters using registry
kubectl patch deployment autocert -n step -p '{"spec":{"template":{"spec":{"containers":[{"name":"autocert","image":"your-registry/autocert-controller:custom"}]}}}}'
```
## 4 Verify Deployment
Check that the controller is running with the new image:

```bash
# Check deployment status
kubectl get deployment autocert -n step

# Check pod is running
kubectl get pods -n step -l app=autocert

# Verify the new image is being used
kubectl describe deployment autocert -n step | grep Image
```

# 5 Verification
After updating the controller image, verify the feature works by:
Deploying a pod with the custom mount path annotation
Checking that certificates are mounted at your specified path
Verifying the application can access certificates at the new location

```bash
# Check if certificates are at custom path
kubectl exec -it <your-pod> -- ls -la /custom/cert/path/
```