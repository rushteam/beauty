module github.com/rushteam/beauty/contrib/memoryvector

go 1.26.0

toolchain go1.26.5

require (
	github.com/rushteam/beauty/contrib/llm v0.0.0
	github.com/rushteam/beauty/contrib/vector v0.0.0
)

replace (
	github.com/rushteam/beauty/contrib/llm => ../llm
	github.com/rushteam/beauty/contrib/vector => ../vector
)
