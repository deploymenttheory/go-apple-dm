-- +up
CREATE TABLE ddm_identities (
    enrollment_id TEXT NOT NULL PRIMARY KEY,
    channel SMALLINT NOT NULL,
    parent_id TEXT NOT NULL
);

CREATE TABLE ddm_declarations (
    identifier   TEXT      NOT NULL PRIMARY KEY,
    type         TEXT      NOT NULL,
    kind         TEXT      NOT NULL,
    server_token TEXT      NOT NULL,
    canonical    BLOB      NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);
CREATE INDEX idx_ddm_declarations_kind ON ddm_declarations (kind, identifier);
CREATE INDEX idx_ddm_declarations_type ON ddm_declarations (type);

CREATE TABLE ddm_declaration_versions (
    identifier   TEXT      NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    server_token TEXT      NOT NULL,
    type         TEXT      NOT NULL,
    canonical    BLOB      NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (identifier, server_token)
);

CREATE TABLE ddm_sets (
    name       TEXT      NOT NULL PRIMARY KEY,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE ddm_set_declarations (
    set_name   TEXT      NOT NULL REFERENCES ddm_sets (name) ON DELETE CASCADE,
    identifier TEXT      NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    added_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (set_name, identifier)
);
CREATE INDEX idx_ddm_set_declarations_identifier ON ddm_set_declarations (identifier);

CREATE TABLE ddm_enrollment_sets (
    enrollment_id TEXT      NOT NULL,
    channel       INTEGER   NOT NULL,
    parent_id     TEXT      NOT NULL DEFAULT '',
    set_name      TEXT      NOT NULL REFERENCES ddm_sets (name) ON DELETE CASCADE,
    assigned_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, set_name)
);
CREATE INDEX idx_ddm_enrollment_sets_set ON ddm_enrollment_sets (set_name, enrollment_id);

CREATE TABLE ddm_enrollment_declarations (
    enrollment_id TEXT      NOT NULL,
    channel       INTEGER   NOT NULL,
    parent_id     TEXT      NOT NULL DEFAULT '',
    identifier    TEXT      NOT NULL REFERENCES ddm_declarations (identifier) ON DELETE CASCADE,
    assigned_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, identifier)
);
CREATE INDEX idx_ddm_enrollment_declarations_identifier ON ddm_enrollment_declarations (identifier, enrollment_id);

CREATE TABLE ddm_snapshots (
    enrollment_id      TEXT      NOT NULL PRIMARY KEY,
    channel            INTEGER   NOT NULL,
    parent_id          TEXT      NOT NULL DEFAULT '',
    declarations_token TEXT      NOT NULL,
    token_changed_at   TIMESTAMP NOT NULL,
    refreshed_at       TIMESTAMP NOT NULL
);

CREATE TABLE ddm_snapshot_items (
    enrollment_id TEXT    NOT NULL REFERENCES ddm_snapshots (enrollment_id) ON DELETE CASCADE,
    kind          TEXT    NOT NULL,
    identifier    TEXT    NOT NULL,
    server_token  TEXT    NOT NULL,
    base_token    TEXT    NOT NULL,
    expanded      BLOB    NULL,
    pos           INTEGER NOT NULL,
    PRIMARY KEY (enrollment_id, kind, identifier)
);
CREATE INDEX idx_ddm_snapshot_items_version ON ddm_snapshot_items (identifier, base_token);

CREATE TABLE ddm_status_declarations (
    enrollment_id TEXT      NOT NULL,
    channel       INTEGER   NOT NULL,
    parent_id     TEXT      NOT NULL DEFAULT '',
    kind          TEXT      NOT NULL,
    identifier    TEXT      NOT NULL,
    server_token  TEXT      NOT NULL,
    active        INTEGER   NOT NULL,
    valid         TEXT      NOT NULL,
    reasons       BLOB      NULL,
    first_seen    TIMESTAMP NOT NULL,
    last_seen     TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, kind, identifier)
);
CREATE INDEX idx_ddm_status_declarations_identifier ON ddm_status_declarations (identifier, enrollment_id);

CREATE TABLE ddm_status_values (
    enrollment_id TEXT      NOT NULL,
    path          TEXT      NOT NULL,
    value         BLOB      NOT NULL,
    first_seen    TIMESTAMP NOT NULL,
    last_seen     TIMESTAMP NOT NULL,
    PRIMARY KEY (enrollment_id, path)
);
CREATE INDEX idx_ddm_status_values_path ON ddm_status_values (path, enrollment_id);

CREATE TABLE ddm_status_errors (
    seq           INTEGER   NOT NULL PRIMARY KEY AUTOINCREMENT,
    enrollment_id TEXT      NOT NULL,
    status_item   TEXT      NOT NULL,
    reasons       BLOB      NULL,
    received_at   TIMESTAMP NOT NULL
);
CREATE INDEX idx_ddm_status_errors_enrollment ON ddm_status_errors (enrollment_id, seq);

CREATE TABLE ddm_status_reports (
    seq           INTEGER   NOT NULL PRIMARY KEY AUTOINCREMENT,
    enrollment_id TEXT      NOT NULL,
    full_report   INTEGER   NOT NULL,
    raw           BLOB      NULL,
    received_at   TIMESTAMP NOT NULL
);
CREATE INDEX idx_ddm_status_reports_enrollment ON ddm_status_reports (enrollment_id, seq);

CREATE TABLE ddm_changes (
    seq             INTEGER   NOT NULL PRIMARY KEY AUTOINCREMENT,
    enrollment_id   TEXT      NOT NULL,
    channel         INTEGER   NOT NULL,
    parent_id       TEXT      NOT NULL DEFAULT '',
    reason          TEXT      NOT NULL,
    created_at      TIMESTAMP NOT NULL,
    attempts        INTEGER   NOT NULL DEFAULT 0,
    last_error      TEXT      NOT NULL,
    next_attempt_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_ddm_changes_due ON ddm_changes (next_attempt_at, seq);
CREATE INDEX idx_ddm_changes_enrollment ON ddm_changes (enrollment_id, seq);

CREATE TABLE ddm_publication_locks (shard INTEGER NOT NULL PRIMARY KEY);
INSERT INTO ddm_publication_locks (shard) VALUES (0), (1), (2), (3), (4), (5), (6), (7), (8), (9), (10), (11), (12), (13), (14), (15), (16), (17), (18), (19), (20), (21), (22), (23), (24), (25), (26), (27), (28), (29), (30), (31), (32), (33), (34), (35), (36), (37), (38), (39), (40), (41), (42), (43), (44), (45), (46), (47), (48), (49), (50), (51), (52), (53), (54), (55), (56), (57), (58), (59), (60), (61), (62), (63), (64), (65), (66), (67), (68), (69), (70), (71), (72), (73), (74), (75), (76), (77), (78), (79), (80), (81), (82), (83), (84), (85), (86), (87), (88), (89), (90), (91), (92), (93), (94), (95), (96), (97), (98), (99), (100), (101), (102), (103), (104), (105), (106), (107), (108), (109), (110), (111), (112), (113), (114), (115), (116), (117), (118), (119), (120), (121), (122), (123), (124), (125), (126), (127), (128), (129), (130), (131), (132), (133), (134), (135), (136), (137), (138), (139), (140), (141), (142), (143), (144), (145), (146), (147), (148), (149), (150), (151), (152), (153), (154), (155), (156), (157), (158), (159), (160), (161), (162), (163), (164), (165), (166), (167), (168), (169), (170), (171), (172), (173), (174), (175), (176), (177), (178), (179), (180), (181), (182), (183), (184), (185), (186), (187), (188), (189), (190), (191), (192), (193), (194), (195), (196), (197), (198), (199), (200), (201), (202), (203), (204), (205), (206), (207), (208), (209), (210), (211), (212), (213), (214), (215), (216), (217), (218), (219), (220), (221), (222), (223), (224), (225), (226), (227), (228), (229), (230), (231), (232), (233), (234), (235), (236), (237), (238), (239), (240), (241), (242), (243), (244), (245), (246), (247), (248), (249), (250), (251), (252), (253), (254), (255);


-- +down
DROP TABLE ddm_publication_locks;
DROP TABLE ddm_changes;
DROP TABLE ddm_status_reports;
DROP TABLE ddm_status_errors;
DROP TABLE ddm_status_values;
DROP TABLE ddm_status_declarations;
DROP TABLE ddm_snapshot_items;
DROP TABLE ddm_snapshots;
DROP TABLE ddm_enrollment_declarations;
DROP TABLE ddm_enrollment_sets;
DROP TABLE ddm_set_declarations;
DROP TABLE ddm_sets;
DROP TABLE ddm_declaration_versions;
DROP TABLE ddm_declarations;
DROP TABLE ddm_identities;
