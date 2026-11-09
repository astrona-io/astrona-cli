*** Settings ***
Documentation       A learner's path through a Kind lab: run, inspect, fail a submission,
...                 solve, pass, pause, resume and destroy. Test cases depend on the ones
...                 before them, so the rest are skipped after the first failure.
Resource            ../resources/astrona.resource
Suite Setup         Start Lifecycle Suite
Suite Teardown      Finish Lifecycle Suite
Test Setup          Skip After Failed Step
Test Teardown       Remember Failed Step
Test Tags           smoke    kind


*** Test Cases ***
Run Creates The Lab
    Run Astrona    run    -c    ${EXAMPLE_LAB}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Exist    ${LAB_NAME}

List Shows The Running Lab
    ${labs}=    Run Astrona Json    list
    Lab Should Be Listed    ${labs}    ${LAB_NAME}

Status Reports The Lab
    ${status}=    Run Astrona Json    status    ${LAB_NAME}
    Log    ${status}

Submit Before Solving Is Graded As Not Passed
    [Documentation]    Exit code 2 means "graded, didn't pass"; anything else is a CLI error.
    Run Astrona    submit    -c    ${EXAMPLE_LAB}    --headless    --keep    rc=2

Submit After Solving Passes
    Run Kubectl On Lab    apply    -f    ${EXAMPLE_LAB}/solution
    Run Astrona    submit    -c    ${EXAMPLE_LAB}    --headless    --keep
    ...    --junit-xml    ${OAT_OUTPUT}/junit-submit-${LAB_NAME}.xml

Stop And Start Keep The Lab
    Run Astrona    stop    ${LAB_NAME}    timeout=${SLOW_TIMEOUT}
    Run Astrona    start    ${LAB_NAME}    timeout=${SLOW_TIMEOUT}
    Run Kubectl On Lab    get    configmap/hello-config    -n    lab-ns

Destroy Removes The Lab
    Run Astrona    destroy    ${LAB_NAME}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${LAB_NAME}


*** Keywords ***
Start Lifecycle Suite
    Kind Cluster Should Not Exist    ${LAB_NAME}
    Prepare Astrona Environment

Finish Lifecycle Suite
    # Best effort: a failed test must not leave a cluster on the runner.
    Run Keyword And Ignore Error    Run Astrona    destroy    ${LAB_NAME}    --headless
    ...    timeout=${SLOW_TIMEOUT}
    Clean Up Astrona Environment
