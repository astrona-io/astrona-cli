*** Settings ***
Documentation       Keywords shared by every acceptance suite: run the binary under test,
...                 check its exit code and read its JSON output.
Library             Process
Library             OperatingSystem
Library             Collections
Library             ../libraries/AstronaLib.py


*** Variables ***
# Binary under test: the ./astrona `just oat` builds. Override with --variable ASTRONA:/path.
${ASTRONA}          ${EXECDIR}/astrona
${REPO_ROOT}        ${EXECDIR}
${EXAMPLE_LAB}      ${REPO_ROOT}/examples/k8s-basics-01
${LAB_NAME}         k8s-basics-01
${OAT_OUTPUT}       ${OUTPUT_DIR}
# The example lab leaves the node image to kind; bundles need one pinned.
# Override with --variable NODE_IMAGE:kindest/node:vX.Y.Z when kind's default moves.
${NODE_IMAGE}       kindest/node:v1.37.0
# Lab creation pulls a kind node image on a cold runner.
${SLOW_TIMEOUT}     15 min
${FAST_TIMEOUT}     2 min


*** Keywords ***
Prepare Astrona Environment
    [Documentation]    Fresh HOME for astrona and the same test settings CI uses.
    File Should Exist    ${ASTRONA}    Build the binary first: just build
    ${env}=    Prepare Isolated Home
    Set Suite Variable    ${ENV}    ${env}
    Run Astrona    settings    set    verbose    false
    Run Astrona    settings    set    keep-context    false
    Run Astrona    settings    set    parallel    1
    Run Astrona    settings    set    update-check    false

Clean Up Astrona Environment
    Remove Isolated Home

Run Astrona
    [Documentation]    Run astrona with ${args} and expect exit code ${rc} (0 by default).
    ...    Output is logged either way, so a failure carries its own evidence.
    ...    --accept-terms is added unless ${accept_terms} is false: with CI set,
    ...    astrona requires it on every command.
    [Arguments]    @{args}    ${rc}=0    ${timeout}=${FAST_TIMEOUT}    ${accept_terms}=${True}
    IF    ${accept_terms}    Append To List    ${args}    --accept-terms
    ${result}=    Run Process    ${ASTRONA}    @{args}
    ...    cwd=${REPO_ROOT}    env=${ENV}    timeout=${timeout}    on_timeout=terminate
    Log    stdout:\n${result.stdout}
    Log    stderr:\n${result.stderr}
    Should Be Equal As Integers    ${result.rc}    ${rc}
    ...    astrona ${args} exited ${result.rc}, expected ${rc}:\n${result.stderr}    values=False
    RETURN    ${result}

Run Astrona Json
    [Documentation]    Run astrona with `-o json` appended and return the parsed stdout.
    [Arguments]    @{args}    ${rc}=0    ${timeout}=${FAST_TIMEOUT}
    ${result}=    Run Astrona    @{args}    -o    json    rc=${rc}    timeout=${timeout}
    ${data}=    Parse Json Output    ${result.stdout}
    RETURN    ${data}

Run Kubectl On Lab
    [Documentation]    kubectl against the lab's isolated kubeconfig (astrona kubeconfig).
    [Arguments]    @{args}
    ${path}=    Run Astrona    kubeconfig    ${LAB_NAME}
    ${result}=    Run Process    kubectl    --kubeconfig    ${path.stdout.strip()}    @{args}
    ...    env=${ENV}    timeout=${FAST_TIMEOUT}
    Log    ${result.stdout}\n${result.stderr}
    Should Be Equal As Integers    ${result.rc}    0    kubectl ${args}: ${result.stderr}    values=False
    RETURN    ${result}
