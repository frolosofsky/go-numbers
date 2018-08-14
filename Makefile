.PHONY: all tests clean

all: numbers

numbers: numbers.go
	go build

tests:
	go test -v

clean:
	rm -f numbers
