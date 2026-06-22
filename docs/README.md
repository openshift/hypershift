Any changes to the docs in this directory can be tested locally before pushing changes to GitHub. Just follow these 
steps:

1. cd to this directory
2. Run `make image` to build the image
3. Run `make build-containerized` to build the containerized version of the image
4. Run `make serve-containerized` to serve up the docs website locally

Any changes you make while the docs are served locally, will be updated in the local docs website.