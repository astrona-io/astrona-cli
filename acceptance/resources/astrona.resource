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
# Base image for the VM labs under acceptance/labs/: Debian 13 genericcloud, a
# dated build (Debian keeps them for years), pinned to the SHA-512 from that
# build's SHA512SUMS. {ARCH} becomes the host's amd64 or arm64. Cached under
# OAT_CACHE_DIR (default ~/.cache/astrona-oat); bump the date and both sums together.
${VM_IMAGE_URL}     https://cloud.debian.org/images/cloud/trixie/20261026-2643/debian-13-genericcloud-{ARCH}-20261026-2643.qcow2
&{VM_IMAGE_SHA512}
...                 amd64=3ace18e33bdd036edb49d1f79342bd01d2ab980e35194edb0b410435cd543538d286a0bd13bbb0835f7862306e3d385ce2f6a70d8ecf0bd961111166adbc1b8e
...                 arm64=71aa5d5dbcb3d9ecfe96bc3f6dd41b6805a0a41c852204a32747d230b64b95912297fb453b11a6031fd54d84a0a0a430e137d07e6986dc9ebc9d41483be003c4
# Lab creation pulls a kind node image on a cold runner.
${SLOW_TIMEOUT}     15 min
${FAST_TIMEOUT}     2 min


*** Keywords ***
Prepare Astrona Environment
    [Documentation]    Fresh HOME for astrona and the same test settings CI uses.
    File Should Exist    ${ASTRONA}    Build the binary first: just build
    ${env}=    Prepare Isolated Home
    Set Suite Variable    ${ENV}    ${env}
    # Output goes to files: Run Process does not drain a pipe while it waits,
    # so anything over the pipe buffer (the catalog JSON is ~100 KB) blocks
    # astrona until the timeout kills it.
    ${capture}=    Evaluate    tempfile.mkdtemp(prefix='astrona-oat-out-')    modules=tempfile
    Set Suite Variable    ${CAPTURE}    ${capture}
    Run Astrona    settings    set    verbose    false
    Run Astrona    settings    set    keep-context    false
    Run Astrona    settings    set    parallel    1
    Run Astrona    settings    set    update-check    false

Clean Up Astrona Environment
    Remove Isolated Home
    Remove Directory    ${CAPTURE}    recursive=True

Run Astrona
    [Documentation]    Run astrona with ${args} and expect exit code ${rc} (0 by default).
    ...    Output is logged either way, so a failure carries its own evidence.
    ...    --accept-terms is added unless ${accept_terms} is false: with CI set,
    ...    astrona requires it on every command. ${stdin} is typed into the
    ...    command, as a learner would into `astrona ssh`.
    [Arguments]    @{args}    ${rc}=0    ${timeout}=${FAST_TIMEOUT}    ${accept_terms}=${True}
    ...    ${stdin}=None
    IF    ${accept_terms}
        # Before `--`, or `astrona shell -- cmd` would pass it on to cmd.
        ${end}=    Get Index From List    ${args}    --
        IF    ${end} < 0
            Append To List    ${args}    --accept-terms
        ELSE
            Insert Into List    ${args}    ${end}    --accept-terms
        END
    END
    ${result}=    Run Process    ${ASTRONA}    @{args}
    ...    cwd=${REPO_ROOT}    env=${ENV}    timeout=${timeout}    on_timeout=terminate
    ...    stdout=${CAPTURE}/stdout.txt    stderr=${CAPTURE}/stderr.txt    stdin=${stdin}
    Log    stdout:\n${result.stdout}
    Log    stderr:\n${result.stderr}
    Should Be Equal As Integers    ${result.rc}    ${rc}
    ...    astrona ${args} exited ${result.rc}, expected ${rc}:\n${result.stderr}    values=False
    RETURN    ${result}

Skip After Failed Step
    [Documentation]    Test Setup for suites whose steps share one lab: each step needs
    ...    what the previous one left behind. Pair with Remember Failed Step.
    ${failed}=    Get Variable Value    ${STEP_FAILED}    ${False}
    Skip If    ${failed}    Skipped: an earlier step in this suite failed

Remember Failed Step
    [Documentation]    Test Teardown paired with Skip After Failed Step.
    IF    "${TEST STATUS}" == "FAIL"    Set Suite Variable    ${STEP_FAILED}    ${True}

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
