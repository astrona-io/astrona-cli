*** Settings ***
Documentation       A signed-in learner: sign in through the device flow in a browser, run a
...                 catalog lab under a lab session, submit it, read progress, destroy it and
...                 sign out. Self-contained: Keycloak runs in a container (podman or docker)
...                 with a throwaway user, and a fake Astrona site stands in for astrona.io
...                 and records what the CLI sends it. No account or secret needed.
Resource            ../resources/astrona.resource
Library             ../libraries/SignInStack.py
Suite Setup         Start Login Suite
Suite Teardown      Finish Login Suite
Test Setup          Skip After Failed Step
Test Teardown       Remember Failed Step
Test Tags           full    login


*** Variables ***
${CATALOG_LAB}      ATS000/section-010/module-01/lab-01
${CATALOG_NAME}     ats-000-lab-010-01


*** Test Cases ***
Login Authorizes This Computer In The Browser
    ${login}=    Start Process    ${ASTRONA}    login    --headless    --site    ${SITE}    --accept-terms
    ...    cwd=${REPO_ROOT}    env=${ENV}    stdout=${CAPTURE}/login.txt    stderr=STDOUT
    ${link}    ${code}=    Wait For Sign In Link    ${CAPTURE}/login.txt
    Authorize In Browser    ${link}    ${code}
    ${result}=    Wait For Process    ${login}    timeout=${FAST_TIMEOUT}    on_timeout=terminate
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    astrona login exited ${result.rc}    values=False
    Should Contain    ${result.stdout}    Signed in
    Site Should Have Received    /api/cli/token    grant=device_code    account=${USERNAME}

Whoami Reports The Account
    ${me}=    Run Astrona Json    whoami
    Should Be True    ${me}[signedIn]
    Should Be Equal    ${me}[username]    ${USERNAME}
    Should Be Equal    ${me}[site]    ${SITE}

Signed In Run Starts A Lab Session
    [Documentation]    Not --headless: that skips the account session this test is about.
    ...    The site saw the request with a token Keycloak issued to the test user.
    [Tags]    kind    network
    ${result}=    Run Astrona    run    ${CATALOG_LAB}    --trust    timeout=${SLOW_TIMEOUT}
    Should Contain    ${result.stdout}    Lab session started for ${USERNAME}
    Kind Cluster Should Exist    ${CATALOG_NAME}
    Site Should Have Received    /api/cli/lab-sessions    account=${USERNAME}    lab=${CATALOG_LAB}

Submit Before Solving Is Graded As Not Passed
    [Documentation]    The first lab ships with a broken image; exit code 2 means graded, not passed.
    ...    --keep: a tracked submit would otherwise delete the lab.
    [Tags]    kind    network
    Run Astrona    submit    ${CATALOG_LAB}    --headless    --keep    rc=2    timeout=${SLOW_TIMEOUT}
    Site Should Have Received    /api/cli/lab-sessions/{id}/results
    ...    account=${USERNAME}    lab=${CATALOG_LAB}    passed=${False}

Progress Lists The Submitted Lab
    [Tags]    kind
    ${progress}=    Run Astrona Json    progress
    Lab Should Have Progress    ${progress}    ${CATALOG_NAME}

Destroy Removes The Catalog Lab
    [Tags]    kind    network
    Run Astrona    destroy    ${CATALOG_NAME}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${CATALOG_NAME}

Logout Signs This Computer Out
    ${result}=    Run Astrona    logout
    Should Contain    ${result.stdout}    Signed out
    Site Should Have Received    /api/cli/revoke    status=${200}
    ${me}=    Run Astrona Json    whoami    rc=1
    Should Not Be True    ${me}[signedIn]


*** Keywords ***
Start Login Suite
    Kind Cluster Should Not Exist    ${CATALOG_NAME}
    ${site}=    Start Sign In Stack
    Set Suite Variable    ${SITE}    ${site}
    ${username}=    Test Username
    Set Suite Variable    ${USERNAME}    ${username}
    Prepare Astrona Environment
    # Every command talks to the fake site, whatever the caller's shell sets.
    Set To Dictionary    ${ENV}    ASTRONA_URL=${SITE}

Finish Login Suite
    # A login still waiting for a browser that never authorized it.
    Terminate All Processes    kill=True
    ${ready}=    Get Variable Value    ${ENV}    ${None}
    IF    $ready is not None
        Run Keyword And Ignore Error    Run Astrona    destroy    ${CATALOG_NAME}    --headless
        ...    timeout=${SLOW_TIMEOUT}
        Run Keyword And Ignore Error    Run Astrona    logout
        Clean Up Astrona Environment
    END
    Stop Sign In Stack

Lab Should Have Progress
    [Documentation]    Progress names a lab by its cluster name, astro(dev)- prefix included.
    [Arguments]    ${progress}    ${lab}
    ${labs}=    Evaluate    [entry.get('lab') for entry in $progress if entry.get('attempts', 0) > 0]
    ${pattern}=    Evaluate    'regexp=^astro(dev)?-' + re.escape($lab) + '$'    modules=re
    Should Contain Match    ${labs}    ${pattern}    msg=${lab} has no attempts in astrona progress: ${labs}
