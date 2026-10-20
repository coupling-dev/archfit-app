// The web app is not Go. This empty module keeps ./... of the App (go test, go vet, golangci-lint) out of
// web/node_modules, where some npm packages ship Go sources.
module github.com/alexei-led/archfit-app/web
