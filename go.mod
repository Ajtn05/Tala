module tala

go 1.25.0

require (
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0
	github.com/gordonklaus/portaudio v0.0.0-20230709114228-aafa478834f5
	golang.org/x/sync v0.11.0
)

require (
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
)

replace github.com/ggerganov/whisper.cpp/bindings/go => ./third_party/whisper.cpp/bindings/go
