CREATE DATABASE IF NOT EXISTS newsdb;
USE newsdb;

CREATE TABLE IF NOT EXISTS articles (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    title       VARCHAR(512)  NOT NULL,
    description TEXT,
    content     TEXT,
    source      VARCHAR(128)  NOT NULL,
    url         VARCHAR(1024) NOT NULL,
    image_url   VARCHAR(1024) DEFAULT '',
    category    VARCHAR(64)   DEFAULT 'general',
    published_at DATETIME     DEFAULT CURRENT_TIMESTAMP,
    created_at  DATETIME      DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY  idx_url (url(512))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_published  ON articles (published_at DESC);
CREATE INDEX idx_category   ON articles (category);
CREATE INDEX idx_source     ON articles (source);
