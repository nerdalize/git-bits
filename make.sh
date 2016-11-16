#!/bin/sh
set -e

function print_help {
	printf "Available Commands:\n";
	awk -v sq="'" '/^function run_([a-zA-Z0-9-]*)\s*/ {print "-e " sq NR "p" sq " -e " sq NR-1 "p" sq }' make.sh \
		| while read line; do eval "sed -n $line make.sh"; done \
		| paste -d"|" - - \
		| sed -e 's/^/  /' -e 's/function run_//' -e 's/#//' -e 's/{/	/' \
		| awk -F '|' '{ print "  " $2 "\t" $1}' \
		| expand -t 30
}

function run_install { #install and update dependencies using glide
	echo "installing..."
	glide install
	echo "cleaning up..."
	rm -fr vendor/github.com/rlmcpherson/s3gof3r/gof3r
}

function run_test { #run test suite of itself and its dependencies
	export $(cat secrets.env)
	terraform apply \
		-var aws_access_key="${AWS_ACCESS_KEY_ID}" \
		-var aws_secret_key="${AWS_SECRET_ACCESS_KEY}"

	export TEST_BUCKET=$(terraform output bucket)
	go test -v bits/*_test.go

	terraform destroy \
		-var aws_access_key="${AWS_ACCESS_KEY_ID}" \
		-var aws_secret_key="${AWS_SECRET_ACCESS_KEY}"
}

function run_build { #build the toolkit as a single binary
	go build -o $GOPATH/bin/git-bits
}

case $1 in
	"install") run_install ;;
	"test") run_test ;;
	"build") run_build ;;

	*) print_help ;;
esac
