*** Settings ***
Documentation       The tools a learner reaches for inside a running Kind lab: the isolated
...                 shell, a soft reset, doctor, diagnose and port forwards. Test cases
...                 share one lab, so the rest are skipped after the first failure.
Resource            ../resources/astrona.resource
Suite Setup         Start Lab Tools Suite
Suite Teardown      Finish Lab Tools Suite
Test Setup          Skip After Failed Step
Test Teardown       Remember Failed Step
Test Tags           full    kind


*** Test Cases ***
Shell Runs A Command In The Isolated Terminal
    ${result}=    Run Astrona    shell    ${LAB_NAME}    --    kubectl    get    namespace    kube-system
    ...    timeout=${SLOW_TIMEOUT}
    Should Contain    ${result.stdout}    kube-system

Shell Starts In A Workspace The Learner Owns
    [Documentation]    The shell opens in /workspace, runs as the workspace's owner and can
    ...    write there. On an SELinux-enforcing host (Fedora) an unlabelled bind
    ...    mount left the learner locked out of their own workspace.
    ${result}=    Run Astrona    shell    ${LAB_NAME}    --    sh    -c
    ...    pwd; [ "$(id -u)" = "$(stat -c %u .)" ] && echo owner; touch .oat && rm .oat && echo writable
    ...    timeout=${SLOW_TIMEOUT}
    Should Start With    ${result.stdout}    /workspace\n
    Should Contain    ${result.stdout}    owner
    Should Contain    ${result.stdout}    writable

Soft Reset Removes The Learner's Work And Keeps The Cluster
    [Documentation]    --soft deletes every namespace created after platform setup.
    Run Kubectl On Lab    apply    -f    ${EXAMPLE_LAB}/solution
    Run Astrona    reset    -c    ${EXAMPLE_LAB}    --soft    --yes    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Exist    ${LAB_NAME}
    ${namespaces}=    Run Kubectl On Lab    get    namespaces    -o    name
    Should Not Contain    ${namespaces.stdout}    namespace/lab-ns

Doctor Reports On The Running Lab
    ${report}=    Run Astrona Json    doctor    -c    ${EXAMPLE_LAB}    timeout=${SLOW_TIMEOUT}
    Log    ${report}

Diagnose Writes A Bundle
    Run Astrona    diagnose    ${LAB_NAME}    -o    ${OAT_OUTPUT}/diagnose-${LAB_NAME}
    ...    timeout=${SLOW_TIMEOUT}
    Directory Should Not Be Empty    ${OAT_OUTPUT}/diagnose-${LAB_NAME}

Port Forwards Can Be Listed
    Run Astrona    port-forward    list    ${LAB_NAME}

Destroy Removes The Lab
    Run Astrona    destroy    ${LAB_NAME}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${LAB_NAME}


*** Keywords ***
Start Lab Tools Suite
    Kind Cluster Should Not Exist    ${LAB_NAME}
    Prepare Astrona Environment
    Run Astrona    run    -c    ${EXAMPLE_LAB}    --headless    timeout=${SLOW_TIMEOUT}

Finish Lab Tools Suite
    Run Keyword And Ignore Error    Run Astrona    destroy    ${LAB_NAME}    --headless
    ...    timeout=${SLOW_TIMEOUT}
    Clean Up Astrona Environment
