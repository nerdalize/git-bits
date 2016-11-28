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

function run_build { #build a development version
	go build -o $GOPATH/bin/git-bits -ldflags "-X main.version=`cat VERSION`"
}

function run_release { #cross compile new release builds
	gox -ldflags "-X main.version=`cat VERSION`" -osarch="linux/amd64 windows/amd64 darwin/amd64" -output=./bin/{{.OS}}_{{.Arch}}/git-bits
}

case $1 in
	"install") run_install ;;
	"test") run_test ;;
	"build") run_build ;;
	"release") run_release ;;

	#
	# following commands are probably not portable
	# and have only been tested on macOS with "github-release"
	# "zip" and "shasum" programs installed and avaiable in PATH

 	# 1. zip all binaries
 	"publish-1" )
		rm -fr bin/dist
		mkdir -p bin/dist

		#move the installers
		for FOLDER in ./bin/*_* ; do \
			NAME=`basename ${FOLDER}`_`cat VERSION` ; \
			ARCHIVE=bin/dist/${NAME}.zip ; \
			pushd ${FOLDER} ; \
			echo Zipping: ${FOLDER}... `pwd` ; \
			zip ../dist/${NAME}.zip ./* ; \
			popd ; \
		done
		;;

	# 2. checksum zips
	"publish-2" )
		rm bin/dist/*_SHA256SUMS || true
		cd bin/dist && shasum -a256 * > ./git-bits_`cat ../../VERSION`_SHA256SUMS
		;;

	# 3. create tag and push it
	"publish-3" )
		git tag v`cat VERSION`
		git push --tags
		;;

	# 4. draft a new release
	"publish-4" )
		github-release release \
    	--user nerdalize \
    	--repo git-bits \
    	--tag v`cat VERSION` \
    	--pre-release
 		;;

 	# 5. upload files
	"publish-5" )
		echo "Uploading zip files..."
		for FOLDER in ./bin/*_* ; do \
			NAME=`basename ${FOLDER}`_`cat VERSION` ; \
			ARCHIVE=bin/dist/${NAME}.zip ; \
			echo "  $ARCHIVE" ; \
			github-release upload \
		    --user nerdalize \
		    --repo git-bits \
		    --tag v`cat VERSION` \
		    --name ${NAME}.zip \
		    --file ${ARCHIVE} ; \
		    echo "done!"; \
		done
		echo "Uploading shasums..."
		github-release upload \
		    --user nerdalize \
		    --repo git-bits \
		    --tag v`cat` \
		    --name git-bits_`cat VERSION`_SHA256SUMS \
		    --file bin/dist/git-bits_`cat VERSION`_SHA256SUMS
		echo "done!"
 		;;

	*) print_help ;;
esac
