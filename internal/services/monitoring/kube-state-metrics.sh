# nano-cloud metrics: kube-state-metrics
# kube-state-metrics reports the cluster's objects (nodes, pods, deployments)
# as metrics. k3s applies whatever is in its manifests directory, and again
# after every restart; the NodePort lets Prometheus reach it from outside.
install -d /var/lib/rancher/k3s/server/manifests
cat > /var/lib/rancher/k3s/server/manifests/nanocloud-kube-state-metrics.yaml <<'YAML'
# Written by nano-cloud for its monitoring.
apiVersion: v1
kind: ServiceAccount
metadata:
  name: nanocloud-kube-state-metrics
  namespace: kube-system
  labels: {app.kubernetes.io/managed-by: nano-cloud}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: nanocloud-kube-state-metrics
  labels: {app.kubernetes.io/managed-by: nano-cloud}
rules:
  - apiGroups: [""]
    resources: [nodes, pods, namespaces, services, persistentvolumeclaims]
    verbs: [list, watch]
  - apiGroups: [apps]
    resources: [deployments, daemonsets, statefulsets, replicasets]
    verbs: [list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: nanocloud-kube-state-metrics
  labels: {app.kubernetes.io/managed-by: nano-cloud}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: nanocloud-kube-state-metrics
subjects:
  - kind: ServiceAccount
    name: nanocloud-kube-state-metrics
    namespace: kube-system
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nanocloud-kube-state-metrics
  namespace: kube-system
  labels: {app.kubernetes.io/managed-by: nano-cloud}
spec:
  replicas: 1
  selector:
    matchLabels: {app.kubernetes.io/name: nanocloud-kube-state-metrics}
  template:
    metadata:
      labels: {app.kubernetes.io/name: nanocloud-kube-state-metrics}
    spec:
      serviceAccountName: nanocloud-kube-state-metrics
      containers:
        - name: kube-state-metrics
          image: registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.13.0
          args:
            - --resources=nodes,pods,namespaces,services,persistentvolumeclaims,deployments,daemonsets,statefulsets,replicasets
          ports:
            - {name: http-metrics, containerPort: 8080}
          resources:
            requests: {cpu: 10m, memory: 32Mi}
            limits: {memory: 128Mi}
          securityContext:
            runAsNonRoot: true
            runAsUser: 65534
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities: {drop: [ALL]}
---
apiVersion: v1
kind: Service
metadata:
  name: nanocloud-kube-state-metrics
  namespace: kube-system
  labels: {app.kubernetes.io/managed-by: nano-cloud}
spec:
  type: NodePort
  selector: {app.kubernetes.io/name: nanocloud-kube-state-metrics}
  ports:
    - {name: http-metrics, port: 8080, targetPort: 8080, nodePort: 32080}
YAML
for _ in $(seq 1 60); do
  if k3s kubectl -n kube-system get deployment nanocloud-kube-state-metrics >/dev/null 2>&1; then break; fi
  sleep 2
done
k3s kubectl -n kube-system rollout status deployment/nanocloud-kube-state-metrics --timeout=300s
ip=$(ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}')
answers "http://$ip:32080/metrics" kube_node_info
