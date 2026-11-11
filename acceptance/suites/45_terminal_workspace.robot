*** Settings ***
Documentation       The isolated terminal's workspace on each container engine the CLI
...                 supports: Docker, rootful Podman and rootless Podman. The job picks the
...                 engine and passes it as ENGINE (docker, podman-rootful,
...                 podman-rootless); the suite checks it got that engine, then that the
...                 shell opens in /workspace as its owner and can write there. On an
...                 SELinux-enforcing host (Fedora) an unlabelled bind mount once left the
...                 learner locked out of their own workspace.
Resource            ../resources/astrona.resource
Suite Setup         Start Terminal Suite
Suite Teardown      Finish Terminal Suite
Test Setup          Skip After Failed Step
Test Teardown       Remember Failed Step
Test Tags           terminal    kind


*** Variables ***
${ENGINE}           docker


*** Test Cases ***
The Lab Runs On The Expected Container Engine
    IF    "${ENGINE}" == "docker"
        ${info}=    Run Process    docker    info    --format    {{.ID}}
        Should Be Equal As Integers    ${info.rc}    0    Docker is not answering: ${info.stderr}
    ELSE
        ${info}=    Run Process    podman    info    --format    {{.Host.Security.Rootless}}
        Should Be Equal As Integers    ${info.rc}    0    Podman is not answering: ${info.stderr}
        ${rootless}=    Set Variable If    "${ENGINE}" == "podman-rootless"    true    false
        Should Be Equal    ${info.stdout.strip()}    ${rootless}
    END
    ${clusters}=    Run Process    kind    get    clusters    env:KIND_EXPERIMENTAL_PROVIDER=${ENGINE.split('-')[0]}
    Should Contain    ${clusters.stdout}    ${LAB_NAME}

Shell Starts In A Workspace The Learner Owns
    ${result}=    Run Astrona    shell    ${LAB_NAME}    --    sh    -c
    ...    pwd; [ "$(id -u)" = "$(stat -c %u .)" ] && echo owner; touch .oat && rm .oat && echo writable
    ...    timeout=${SLOW_TIMEOUT}
    Should Start With    ${result.stdout}    /workspace\n
    Should Contain    ${result.stdout}    owner
    Should Contain    ${result.stdout}    writable

The Learner's Files Stay On The Host
    [Documentation]    What the shell writes to /workspace is in the lab's workspace folder
    ...    on the host, owned by the user who runs astrona.
    Run Astrona    shell    ${LAB_NAME}    --    sh    -c    echo kept > notes.txt
    ${notes}=    Run Process    find    ${ENV}[HOME]    -path    */work/notes.txt    -user    ${ENV}[USER_ID]
    Should Not Be Empty    ${notes.stdout}    notes.txt is missing from the host workspace or has another owner

Destroy Removes The Lab
    Run Astrona    destroy    ${LAB_NAME}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${LAB_NAME}


*** Keywords ***
Start Terminal Suite
    Kind Cluster Should Not Exist    ${LAB_NAME}
    Prepare Astrona Environment
    ${uid}=    Evaluate    str(os.getuid())    modules=os
    Set To Dictionary    ${ENV}    USER_ID=${uid}
    Run Astrona    run    -c    ${EXAMPLE_LAB}    --headless    timeout=${SLOW_TIMEOUT}

Finish Terminal Suite
    Run Keyword And Ignore Error    Run Astrona    destroy    ${LAB_NAME}    --headless
    ...    timeout=${SLOW_TIMEOUT}
    Clean Up Astrona Environment
