{{if or .EnableWeb .EnableGrpc}}apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}
  labels:
    app: {{.Name}}
spec:
  selector:
    app: {{.Name}}
  ports:
{{if .EnableWeb}}    - name: http
      port: 8080
      targetPort: http
{{end}}{{if .EnableGrpc}}    - name: grpc
      port: 9090
      targetPort: grpc
{{end}}{{end}}