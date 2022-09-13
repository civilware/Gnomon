// Copyright 2022 Civilware. All rights reserved.
// Gnomon - DERO Network Indexer (https://github.com/civilware/Gnomon)
// Usernames: Gnomon, gnomon

Function InitializePrivate() Uint64
    10  IF EXISTS("owner") == 0 THEN GOTO 30
    20  RETURN 1
    30  STORE("owner", SIGNER())
    40  STORE("signature", "")
    50  STORE("balance", 0)

    100 RETURN 0
End Function

Function InputSCID(scid String, scowner String, deployheight Uint64) Uint64
    10  IF LOAD("owner") == SIGNER() THEN GOTO 20 ELSE GOTO 100
    20  IF EXISTS(scid) == 0 THEN GOTO 30 ELSE GOTO 100
    30  IF scowner != "" THEN GOTO 40 ELSE GOTO 100
    40  IF IS_ADDRESS_VALID(ADDRESS_RAW(scowner)) == 1 THEN GOTO 50 ELSE GOTO 100

    50  STORE(scid, "")
    60  STORE(scid + "owner", ADDRESS_RAW(scowner))
    70  STORE(scid + "height", deployheight)

    100 RETURN 0
End Function

Function RemoveSCID(scid String) Uint64
    10  IF LOAD("owner") == SIGNER() THEN GOTO 20 ELSE GOTO 100
    20  IF EXISTS(scid) == 1 THEN GOTO 30 ELSE GOTO 100
    30  DELETE(scid)
    40  DELETE(scid + "owner")
    50  DELETE(scid + "height")

    100 RETURN 0
End Function

Function SetSCIDHeaders(scid String, name String, descr String, icon String) Uint64
    10  IF EXISTS(scid + "owner") == 1 THEN GOTO 20 ELSE GOTO 100
    20  IF LOAD(scid + "owner") == SIGNER() THEN GOTO 30 ELSE GOTO 100
    30  IF DEROVALUE() < 200 THEN GOTO 100
    40  STORE("balance", LOAD("balance") + DEROVALUE())
    50  STORE(scid, name + ";" + descr + ";" + icon)

    100 RETURN 0
End Function

Function Withdraw() Uint64
    10  IF LOAD("owner") == SIGNER() THEN GOTO 20 ELSE GOTO 100
    20  IF LOAD("balance") > 0 THEN GOTO 30 ELSE GOTO 100
    30  SEND_DERO_TO_ADDRESS(SIGNER(), LOAD("balance"))
    40  STORE("balance", 0)

    100 RETURN 0
End Function

Function UpdateSignature(SC_SIG String) Uint64
    10  IF LOAD("owner") == SIGNER() THEN GOTO 20 ELSE GOTO 100
    20  IF SC_SIG != "" THEN GOTO 30 ELSE GOTO 100
    30  STORE("signature", SC_SIG)

    100 RETURN 0
End Function

Function UpdateCode(SC_CODE String, SC_SIG String) Uint64
    10  IF LOAD("owner") == SIGNER() THEN GOTO 20 ELSE GOTO 100
    20  IF SC_CODE != "" THEN GOTO 30 ELSE GOTO 100
    30  UPDATE_SC_CODE(SC_CODE)
    40  IF SC_SIG != "" THEN GOTO 50 ELSE GOTO 100
    50  STORE("signature", SC_SIG)

    100 RETURN 0
End Function