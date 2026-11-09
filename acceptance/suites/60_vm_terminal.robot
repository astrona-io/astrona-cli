*** Settings ***
Documentation       A learner's path through a VM lab with a terminal: run a QEMU VM, inspect
...                 it, fail a submission, solve it over `astrona ssh`, pass and destroy.
...                 Needs qemu with hardware acceleration (KVM or HVF) and downloads a
...                 Debian cloud image once. Test cases depend on the ones before them,
...                 so the rest are skipped after the first failure.
Resource            ../resources/astrona.resource
Suite Setup         Start VM Terminal Suite
Suite Teardown      Finish VM Terminal Suite
Test Setup          Skip After Failed Step
Test Teardown       Remember Failed Step
Test Tags           full    vm


*** Variables ***
${VM_LAB}           oat-vm-terminal


*** Test Cases ***
Run Boots The VM
    Run Astrona    run    -c    ${VM_LAB_DIR}    --headless    timeout=${SLOW_TIMEOUT}
    VM Should Be Running    ${VM_LAB}

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
    [Documentation]    The learner types the answer into the VM's terminal.
    Run Astrona    ssh    ${VM_LAB}    stdin=echo solved > /tmp/astrona-lab/answer.txt\nexit\n
    Run Astrona    submit    -c    ${VM_LAB_DIR}    --headless    --keep
    ...    --junit-xml    ${OAT_OUTPUT}/junit-submit-${VM_LAB}.xml

Stop Is Refused And Keeps The VM Running
    [Documentation]    stop/start pause kind labs only; a VM lab must be left as it is.
    ${result}=    Run Astrona    stop    ${VM_LAB}    rc=1
    Should Contain    ${result.stderr}    is a qemu lab
    VM Should Be Running    ${VM_LAB}

Destroy Removes The VM
    Run Astrona    destroy    ${VM_LAB}    --headless    timeout=${SLOW_TIMEOUT}
    VM Should Be Gone    ${VM_LAB}


*** Keywords ***
Start VM Terminal Suite
    VM Should Not Be Running    ${VM_LAB}
    Prepare Astrona Environment
    ${dir}=    Prepare VM Lab    ${REPO_ROOT}/acceptance/labs/vm-terminal    ${CAPTURE}/lab
    ...    ${VM_IMAGE_URL}    ${VM_IMAGE_SHA512}
    Set Suite Variable    ${VM_LAB_DIR}    ${dir}

Finish VM Terminal Suite
    # Best effort: a failed test must not leave a VM running on the machine.
    Run Keyword And Ignore Error    Run Astrona    destroy    ${VM_LAB}    --headless
    ...    timeout=${SLOW_TIMEOUT}
    Clean Up Astrona Environment
