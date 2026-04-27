///////////////////////////////////////////////////////////////////////////////
// forge-operator — CI/CD Pipeline (Jenkins)
//
// Stages:  Checkout → Tooling → Lint → Test → Build → Scan → Push → Helm Package
//
// Version derived from git tag (v0.3.0 → 0.3.0); commit SHA otherwise.
//
// Jenkins credentials required:
//   - forge-harbor-creds: Harbor username/password (registry.cloudforyour.work)
///////////////////////////////////////////////////////////////////////////////

pipeline {
    agent any

    environment {
        GO_VERSION       = '1.23.4'
        K8S_VERSION      = '1.30'
        DOCKER_BUILDKIT  = '1'

        DOCKER_REGISTRY  = 'registry.cloudforyour.work'
        OPERATOR_IMAGE   = "${DOCKER_REGISTRY}/forge-platform/forge-operator"

        VERSION          = sh(script: '''
            TAG=$(git describe --tags --exact-match 2>/dev/null || echo "")
            if [ -n "$TAG" ]; then echo "${TAG#v}"; else git rev-parse --short HEAD; fi
        ''', returnStdout: true).trim()
        IS_TAG_BUILD     = sh(script: 'git describe --tags --exact-match 2>/dev/null && echo true || echo false', returnStdout: true).trim()

        GOPATH           = "${env.WORKSPACE}/.go"
        PATH             = "${env.WORKSPACE}/.go/bin:/usr/local/go/bin:${env.PATH}"
    }

    options {
        buildDiscarder(logRotator(numToKeepStr: '20'))
        timestamps()
        timeout(time: 30, unit: 'MINUTES')
        disableConcurrentBuilds()
    }

    stages {
        stage('Info') {
            steps {
                echo "Version:      ${VERSION}"
                echo "Tag build:    ${IS_TAG_BUILD}"
                echo "Image:        ${OPERATOR_IMAGE}:${VERSION}"
                sh 'go version || echo "go missing — install /usr/local/go before running"'
            }
        }

        stage('Tooling') {
            steps {
                sh '''
                    set -eu
                    mkdir -p $GOPATH/bin
                    # setup-envtest for spinning up apiserver+etcd in tests
                    if ! command -v setup-envtest >/dev/null; then
                        go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
                    fi
                    # golangci-lint for code quality
                    if ! command -v golangci-lint >/dev/null; then
                        curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
                            | sh -s -- -b $GOPATH/bin v1.61.0
                    fi
                    setup-envtest use $K8S_VERSION -p path > /tmp/envtest-path
                '''
            }
        }

        stage('Lint') {
            steps {
                sh '''
                    set -eu
                    go vet ./...
                    golangci-lint run --timeout=5m --out-format=line-number
                '''
            }
        }

        stage('Unit + e2e tests') {
            steps {
                sh '''
                    set -eu
                    export KUBEBUILDER_ASSETS=$(cat /tmp/envtest-path)
                    go test ./... -v -count=1 -timeout=300s -coverprofile=coverage.out
                    go tool cover -func=coverage.out | tail -5
                '''
            }
            post {
                always {
                    junit allowEmptyResults: true, testResults: '**/test-results.xml'
                    archiveArtifacts artifacts: 'coverage.out', allowEmptyArchive: true
                }
            }
        }

        stage('Build image') {
            steps {
                sh '''
                    set -eu
                    docker build \
                        --build-arg VERSION=${VERSION} \
                        -t ${OPERATOR_IMAGE}:${VERSION} \
                        -t ${OPERATOR_IMAGE}:latest \
                        .
                '''
            }
        }

        stage('Security scan') {
            steps {
                sh '''
                    set -eu
                    # Trivy: filesystem scan (deps) + image scan (final binary)
                    if ! command -v trivy >/dev/null; then
                        curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin
                    fi
                    trivy fs --severity HIGH,CRITICAL --exit-code 0 --no-progress .
                    trivy image --severity HIGH,CRITICAL --exit-code 0 --no-progress ${OPERATOR_IMAGE}:${VERSION}
                '''
            }
        }

        stage('Push image') {
            when {
                anyOf {
                    branch 'main'
                    expression { IS_TAG_BUILD == 'true' }
                }
            }
            steps {
                withCredentials([usernamePassword(credentialsId: 'forge-harbor-creds',
                                                  usernameVariable: 'HARBOR_USER',
                                                  passwordVariable: 'HARBOR_PASS')]) {
                    sh '''
                        set -eu
                        echo "$HARBOR_PASS" | docker login ${DOCKER_REGISTRY} -u "$HARBOR_USER" --password-stdin
                        docker push ${OPERATOR_IMAGE}:${VERSION}
                        if [ "$IS_TAG_BUILD" = "true" ]; then
                            docker push ${OPERATOR_IMAGE}:latest
                        fi
                    '''
                }
            }
        }

        stage('Helm package') {
            when {
                expression { IS_TAG_BUILD == 'true' }
            }
            steps {
                sh '''
                    set -eu
                    if ! command -v helm >/dev/null; then
                        curl -sfL https://get.helm.sh/helm-v3.16.2-linux-amd64.tar.gz \
                            | tar -xz -C /tmp \
                            && sudo install -m 755 /tmp/linux-amd64/helm /usr/local/bin/helm
                    fi
                    helm lint helm
                    helm package helm --version ${VERSION} --app-version ${VERSION} -d dist/
                    ls -la dist/
                '''
                archiveArtifacts artifacts: 'dist/*.tgz', allowEmptyArchive: false
            }
        }
    }

    post {
        always {
            sh 'docker logout ${DOCKER_REGISTRY} || true'
        }
        success {
            echo "Build ${VERSION} succeeded."
        }
        failure {
            echo "Build ${VERSION} failed."
        }
    }
}
