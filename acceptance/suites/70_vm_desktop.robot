*** Settings ***
Documentation       A learner's path through a VM lab as a desktop: the VM opens its own
...                 window (display: true), which is the desktop astrona offers today. Run,
...                 check the window, fail a submission, solve, pass and destroy. Needs
...                 qemu with hardware acceleration and a display: the macOS desktop, or
...                 X on Linux (CI runs under xvfb-run). Test cases depend on the ones
...                 before them, so the rest are skipped after the first failure.
Resource            ../resources/astrona.resource
Suite Setup         Start VM Desktop Suite
Suite Teardown      Finish VM Desktop Suite
Test Setup          Skip After Failed Step
Test Teardown       Remember Failed Step
Test Tags           full    vm    desktop


*** Variables ***
${VM_LAB}           oat-vm-desktop


*** Test Cases ***
Run Boots The VM
    Run Astrona    run    -c    ${VM_LAB_DIR}    --headless    timeout=${SLOW_TIMEOUT}
    VM Should Be Running    ${VM_LAB}

The VM Opens A Desktop Window
    [Documentation]    --headless only means astrona asks nothing; the VM still gets its window.
    Desktop Window Should Be Open    ${VM_LAB}

List Shows The Running VM
    ${labs}=    Run Astrona Json    list
    Lab Should Be Listed    ${labs}    ${VM_LAB}

Status Reports The VM
    ${status}=    Run Astrona Json    status    ${VM_LAB}
    Log    ${status}

Submit Before Solving Is Graded As Not Passed
    [Documentation]    Exit code 2 means "graded, didn't pass"; anything else is a CLI error.
    Run Astrona    submit    -c    ${VM_LAB_DIR}    --headless    --keep    rc=2

Submit After Solving Passes
    [Documentation]    The window has no input devices yet, so the answer goes in over SSH.
    Run Astrona    ssh    ${VM_LAB}    stdin=echo solved > /tmp/astrona-lab/answer.txt\nexit\n
    Run Astrona    submit    -c    ${VM_LAB_DIR}    --headless    --keep
    ...    --junit-xml    ${OAT_OUTPUT}/junit-submit-${VM_LAB}.xml

Stop Is Refused And Keeps The VM Running
    [Documentation]    stop/start pause kind labs only; a VM lab must be left as it is.
    ${result}=    Run Astrona    stop    ${VM_LAB}    rc=1
    Should Contain    ${result.stderr}    is a qemu lab
    Desktop Window Should Be Open    ${VM_LAB}

Destroy Removes The VM And Its Window
    Run Astrona    destroy    ${VM_LAB}    --headless    timeout=${SLOW_TIMEOUT}
    VM Should Be Gone    ${VM_LAB}


*** Keywords ***
Start VM Desktop Suite
    VM Should Not Be Running    ${VM_LAB}
    Prepare Astrona Environment
    ${dir}=    Prepare VM Lab    ${REPO_ROOT}/acceptance/labs/vm-desktop    ${CAPTURE}/lab
    ...    ${VM_IMAGE_URL}    ${VM_IMAGE_SHA512}
    Set Suite Variable    ${VM_LAB_DIR}    ${dir}

Finish VM Desktop Suite
    # Best effort: a failed test must not leave a VM running on the machine.
    Run Keyword And Ignore Error    Run Astrona    destroy    ${VM_LAB}    --headless
    ...    timeout=${SLOW_TIMEOUT}
    Clean Up Astrona Environment
