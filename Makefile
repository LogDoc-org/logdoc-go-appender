NAME=logdoc-go-appender
GOFLAGS=-buildmode=plugin

all: $(NAME).so

clean:
	rm -f $(NAME).so

$(NAME).so: main.go
	go build $(GOFLAGS) -o $(NAME).so .