module tala

go 1.25

require (
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0
	github.com/gordonklaus/portaudio v0.0.0-20230709114228-aafa478834f5
	golang.org/x/sync v0.11.0
)

replace github.com/ggerganov/whisper.cpp/bindings/go => ./third_party/whisper.cpp/bindings/go
