apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.Name}}
  labels:
    app: {{.Name}}
spec:
  replicas: 2
  selector:
    matchLabels:
      app: {{.Name}}
  template:
    metadata:
      labels:
        app: {{.Name}}
    spec:
      containers:
        - name: {{.Name}}
          image: {{.Name}}:latest
          imagePullPolicy: IfNotPresent
{{if or .EnableWeb .EnableGrpc}}          ports:
{{if .EnableWeb}}            - name: http
              containerPort: 8080
{{end}}{{if .EnableGrpc}}            - name: grpc
              containerPort: 9090
{{end}}{{end}}          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 256Mi
