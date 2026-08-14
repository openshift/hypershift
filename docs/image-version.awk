BEGIN { FS = "==" }; /^mkdocs-material==/{split($2, p, " "); print p[1]}
