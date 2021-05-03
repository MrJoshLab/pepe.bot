node {

    checkout scm

    docker.withRegistry('https://registry.hub.docker.com', 'dockerhub') {

        def image = docker.build("iamalirezaj/pepebot:from-jenkins")

        /* Push the container to the custom Registry */
        image.push()
    }
}
