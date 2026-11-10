*** Settings ***
Documentation       A learner's path through a Kind lab: run, inspect, pause, resume and
...                 destroy; then run again and fail a submission, and run once more, solve
...                 and pass. Every submit ends the attempt and deletes the lab, pass or
...                 fail. Test cases depend on the ones before them, so the rest are skipped
...                 after the first failure.
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

Stop And Start Keep The Lab
    Run Astrona    stop    ${LAB_NAME}    timeout=${SLOW_TIMEOUT}
    Run Astrona    start    ${LAB_NAME}    timeout=${SLOW_TIMEOUT}
    Run Kubectl On Lab    get    namespace    kube-system

Destroy Removes The Lab
    Run Astrona    destroy    ${LAB_NAME}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${LAB_NAME}

Submit Before Solving Is Graded As Not Passed And Ends The Attempt
    [Documentation]    Exit code 2 means "graded, didn't pass"; anything else is a CLI error.
    ...    A failed submit is final too: the lab is deleted.
    Run Astrona    run    -c    ${EXAMPLE_LAB}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Exist    ${LAB_NAME}
    Run Astrona    submit    -c    ${EXAMPLE_LAB}    --headless    rc=2    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${LAB_NAME}

Submit After Solving Passes And Ends The Attempt
    Run Astrona    run    -c    ${EXAMPLE_LAB}    --headless    timeout=${SLOW_TIMEOUT}
    Run Kubectl On Lab    apply    -f    ${EXAMPLE_LAB}/solution
    Run Astrona    submit    -c    ${EXAMPLE_LAB}    --headless
    ...    --junit-xml    ${OAT_OUTPUT}/junit-submit-${LAB_NAME}.xml    timeout=${SLOW_TIMEOUT}
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
