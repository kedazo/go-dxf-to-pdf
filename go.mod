module github.com/kedazo/go-dxf-to-pdf

go 1.25.0

replace github.com/ixmilia/dxf-go => github.com/kedazo/dxf-go v0.2.1

require (
	github.com/alecthomas/kong v1.14.0
	github.com/go-pdf/fpdf v0.9.0
	github.com/ixmilia/dxf-go v0.0.0-00010101000000-000000000000
	golang.org/x/text v0.34.0
)

require github.com/google/uuid v1.6.0 // indirect
