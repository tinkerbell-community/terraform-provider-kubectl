#
# Given the following YAML template
#
apiVersion: v1
kind: Pod
metadata:
  name: nginx
  labels:
    name: nginx
spec:
  containers:
  - name: nginx
    image: %{ if docker_image != "" }${docker_image}%{ else }default-nginx%{ endif }
    ports:
    - containerPort: 80


#
# Load the yaml file, parsing the ${docker_registry} variable, resulting in `default-nginx`
#
data "kubectl_path_documents" "manifests" {
    pattern = "./manifests/*.yaml"
    vars = {
        docker_image = ""
    }
}
