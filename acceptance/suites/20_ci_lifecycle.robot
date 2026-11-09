*** Settings ***
Documentation       `astrona test`, the command lab authors run in their own CI: bootstrap,
...                 reference solution, grading and teardown in one go.
Resource            ../resources/astrona.resource
Suite Setup         Start CI Lifecycle Suite
Suite Teardown      Clean Up Astrona Environment
Test Tags           smoke    kind


*** Test Cases ***
Astrona Test Runs The Full Lifecycle
    Run Astrona    test    -c    ${EXAMPLE_LAB}    --headless
    ...    --junit-xml    ${OAT_OUTPUT}/junit-test-${LAB_NAME}.xml
    ...    --diagnostics-dir    ${OAT_OUTPUT}/diagnostics
    ...    timeout=${SLOW_TIMEOUT}
    File Should Exist    ${OAT_OUTPUT}/junit-test-${LAB_NAME}.xml
    Kind Cluster Should Not Exist    ${LAB_NAME}


*** Keywords ***
Start CI Lifecycle Suite
    Kind Cluster Should Not Exist    ${LAB_NAME}
    Prepare Astrona Environment
