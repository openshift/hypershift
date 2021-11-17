{{ define "packages" }}
---
title: API Reference
---

# HyperShift API Reference

{{ with .packages}}
<p>Packages:</p>
<ul>
    {{ range . }}
    <li>
        <a href="#{{- packageAnchorID . -}}">{{ packageDisplayName . }}</a>
    </li>
    {{ end }}
</ul>
{{ end}}

{{ range .packages }}
    <h2 id="{{- packageAnchorID . -}}">
        {{- packageDisplayName . -}}
    </h2>

    {{ with (index .GoPackages 0 )}}
        {{ with .DocComments }}
        <p>
            {{ safe (renderComments .) }}
        </p>
        {{ end }}
    {{ end }}

    {{ range (visibleTypes (sortedTypes .Types))}}
        {{ template "type" .  }}
    {{ end }}
    <hr/>
{{ end }}

{{ end }}
