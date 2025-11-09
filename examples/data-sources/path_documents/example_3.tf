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
    image: ${docker_image}
    ports:
    - containerPort: 80


#
# Load the yaml file, parsing the ${docker_image} variable
#
data "kubectl_path_documents" "manifests" {
    pattern = "./manifests/*.yaml"
    vars = {
        docker_image = "https://myregistry.example.com/nginx"
    }
}
