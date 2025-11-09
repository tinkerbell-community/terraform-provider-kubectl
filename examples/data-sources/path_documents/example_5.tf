#
# Given the following YAML template
#
%{ for namespace in split(",", namespaces) }
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: myvolume-claim
  namespace: ${namespace}
spec:
  accessModes:
    - ReadWriteMany
  volumeMode: Filesystem
  resources:
    requests:
      storage: 100Gi
%{ endfor }

#
# Loading the document is a comma-separated list of namespace
#
data "kubectl_path_documents" "manifests" {
    pattern = "./manifests/*.yaml"
    vars = {
        namespaces = "dev,test,prod"
    }
}

#
# Results in 3 documents:
#
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: myvolume-claim
  namespace: dev
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: myvolume-claim
  namespace: test
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: myvolume-claim
  namespace: prod
