*** Settings ***
Documentation       The published catalog, signed out: browse it, then run and destroy the
...                 first ATS000 lab without an account. Needs network access to astrona.io.
Resource            ../resources/astrona.resource
Suite Setup         Start Catalog Suite
Suite Teardown      Finish Catalog Suite
Test Setup          Skip After Failed Step
Test Teardown       Remember Failed Step
Test Tags           full    kind    network


*** Variables ***
${CATALOG_LAB}      ATS000/section-010/module-01/lab-01
${CATALOG_NAME}     ats-000-lab-010-01


*** Test Cases ***
Catalog Lists Trainings
    ${catalog}=    Run Astrona Json    labs    --refresh
    Should Not Be Empty    ${catalog}

Signed Out Run Needs Explicit Confirmation
    [Documentation]    Without --without-login a signed-out run must not start silently.
    # Not --headless: that skips the sign-in reminder this test is about.
    Run Astrona    run    ${CATALOG_LAB}    --ui\=false    --trust    rc=1    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${CATALOG_NAME}

Signed Out Run Creates The Lab
    Run Astrona    run    ${CATALOG_LAB}    --headless    --trust    --without-login
    ...    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Exist    ${CATALOG_NAME}

Destroy Removes The Catalog Lab
    Run Astrona    destroy    ${CATALOG_NAME}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${CATALOG_NAME}


*** Keywords ***
Start Catalog Suite
    Kind Cluster Should Not Exist    ${CATALOG_NAME}
    Prepare Astrona Environment

Finish Catalog Suite
    Run Keyword And Ignore Error    Run Astrona    destroy    ${CATALOG_NAME}    --headless
    ...    timeout=${SLOW_TIMEOUT}
    Clean Up Astrona Environment
