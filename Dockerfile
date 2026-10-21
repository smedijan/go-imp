FROM golang:1.25

COPY imp /usr/bin
RUN adduser --disabled-password --gecos "" nonroot
USER nonroot:nonroot

CMD ["imp"]