// Code generated by goyacc -o parser.go -p Beauty parser.go.y. DO NOT EDIT.

//line parser.go.y:2

package parser

import __yyfmt__ "fmt"

//line parser.go.y:3

import (
	"fmt"
)

//line parser.go.y:12
type BeautySymType struct {
	yys int
	// empty struct{}
	val string
}

const ILLEGAL = 57346
const EOF = 57347
const At = 57348
const at_auth = 57349
const at_val = 57350
const string = 57351

var BeautyToknames = [...]string{
	"$end",
	"error",
	"$unk",
	"ILLEGAL",
	"EOF",
	"At",
	"at_auth",
	"at_val",
	"string",
}

var BeautyStatenames = [...]string{}

const BeautyEofCode = 1
const BeautyErrCode = 2
const BeautyInitialStackSize = 16

//line parser.go.y:36

/*  start  of  programs  */
//line yacctab:1
var BeautyExca = [...]int{
	-1, 1,
	1, -1,
	-2, 0,
}

const BeautyPrivate = 57344

const BeautyLast = 3

var BeautyAct = [...]int{
	3, 2, 1,
}

var BeautyPact = [...]int{
	-6, -1000, -8, -1000,
}

var BeautyPgo = [...]int{
	0, 2, 2,
}

var BeautyR1 = [...]int{
	0, 1, 2,
}

var BeautyR2 = [...]int{
	0, 2, 1,
}

var BeautyChk = [...]int{
	-1000, -1, 7, 8,
}

var BeautyDef = [...]int{
	0, -2, 0, 1,
}

var BeautyTok1 = [...]int{
	1,
}

var BeautyTok2 = [...]int{
	2, 3, 4, 5, 6, 7, 8, 9,
}

var BeautyTok3 = [...]int{
	0,
}

var BeautyErrorMessages = [...]struct {
	state int
	token int
	msg   string
}{}

//line yaccpar:1

/*	parser for yacc output	*/

var (
	BeautyDebug        = 0
	BeautyErrorVerbose = false
)

type BeautyLexer interface {
	Lex(lval *BeautySymType) int
	Error(s string)
}

type BeautyParser interface {
	Parse(BeautyLexer) int
	Lookahead() int
}

type BeautyParserImpl struct {
	lval  BeautySymType
	stack [BeautyInitialStackSize]BeautySymType
	char  int
}

func (p *BeautyParserImpl) Lookahead() int {
	return p.char
}

func BeautyNewParser() BeautyParser {
	return &BeautyParserImpl{}
}

const BeautyFlag = -1000

func BeautyTokname(c int) string {
	if c >= 1 && c-1 < len(BeautyToknames) {
		if BeautyToknames[c-1] != "" {
			return BeautyToknames[c-1]
		}
	}
	return __yyfmt__.Sprintf("tok-%v", c)
}

func BeautyStatname(s int) string {
	if s >= 0 && s < len(BeautyStatenames) {
		if BeautyStatenames[s] != "" {
			return BeautyStatenames[s]
		}
	}
	return __yyfmt__.Sprintf("state-%v", s)
}

func BeautyErrorMessage(state, lookAhead int) string {
	const TOKSTART = 4

	if !BeautyErrorVerbose {
		return "syntax error"
	}

	for _, e := range BeautyErrorMessages {
		if e.state == state && e.token == lookAhead {
			return "syntax error: " + e.msg
		}
	}

	res := "syntax error: unexpected " + BeautyTokname(lookAhead)

	// To match Bison, suggest at most four expected tokens.
	expected := make([]int, 0, 4)

	// Look for shiftable tokens.
	base := BeautyPact[state]
	for tok := TOKSTART; tok-1 < len(BeautyToknames); tok++ {
		if n := base + tok; n >= 0 && n < BeautyLast && BeautyChk[BeautyAct[n]] == tok {
			if len(expected) == cap(expected) {
				return res
			}
			expected = append(expected, tok)
		}
	}

	if BeautyDef[state] == -2 {
		i := 0
		for BeautyExca[i] != -1 || BeautyExca[i+1] != state {
			i += 2
		}

		// Look for tokens that we accept or reduce.
		for i += 2; BeautyExca[i] >= 0; i += 2 {
			tok := BeautyExca[i]
			if tok < TOKSTART || BeautyExca[i+1] == 0 {
				continue
			}
			if len(expected) == cap(expected) {
				return res
			}
			expected = append(expected, tok)
		}

		// If the default action is to accept or reduce, give up.
		if BeautyExca[i+1] != 0 {
			return res
		}
	}

	for i, tok := range expected {
		if i == 0 {
			res += ", expecting "
		} else {
			res += " or "
		}
		res += BeautyTokname(tok)
	}
	return res
}

func Beautylex1(lex BeautyLexer, lval *BeautySymType) (char, token int) {
	token = 0
	char = lex.Lex(lval)
	if char <= 0 {
		token = BeautyTok1[0]
		goto out
	}
	if char < len(BeautyTok1) {
		token = BeautyTok1[char]
		goto out
	}
	if char >= BeautyPrivate {
		if char < BeautyPrivate+len(BeautyTok2) {
			token = BeautyTok2[char-BeautyPrivate]
			goto out
		}
	}
	for i := 0; i < len(BeautyTok3); i += 2 {
		token = BeautyTok3[i+0]
		if token == char {
			token = BeautyTok3[i+1]
			goto out
		}
	}

out:
	if token == 0 {
		token = BeautyTok2[1] /* unknown char */
	}
	if BeautyDebug >= 3 {
		__yyfmt__.Printf("lex %s(%d)\n", BeautyTokname(token), uint(char))
	}
	return char, token
}

func BeautyParse(Beautylex BeautyLexer) int {
	return BeautyNewParser().Parse(Beautylex)
}

func (Beautyrcvr *BeautyParserImpl) Parse(Beautylex BeautyLexer) int {
	var Beautyn int
	var BeautyVAL BeautySymType
	var BeautyDollar []BeautySymType
	_ = BeautyDollar // silence set and not used
	BeautyS := Beautyrcvr.stack[:]

	Nerrs := 0   /* number of errors */
	Errflag := 0 /* error recovery flag */
	Beautystate := 0
	Beautyrcvr.char = -1
	Beautytoken := -1 // Beautyrcvr.char translated into internal numbering
	defer func() {
		// Make sure we report no lookahead when not parsing.
		Beautystate = -1
		Beautyrcvr.char = -1
		Beautytoken = -1
	}()
	Beautyp := -1
	goto Beautystack

ret0:
	return 0

ret1:
	return 1

Beautystack:
	/* put a state and value onto the stack */
	if BeautyDebug >= 4 {
		__yyfmt__.Printf("char %v in %v\n", BeautyTokname(Beautytoken), BeautyStatname(Beautystate))
	}

	Beautyp++
	if Beautyp >= len(BeautyS) {
		nyys := make([]BeautySymType, len(BeautyS)*2)
		copy(nyys, BeautyS)
		BeautyS = nyys
	}
	BeautyS[Beautyp] = BeautyVAL
	BeautyS[Beautyp].yys = Beautystate

Beautynewstate:
	Beautyn = BeautyPact[Beautystate]
	if Beautyn <= BeautyFlag {
		goto Beautydefault /* simple state */
	}
	if Beautyrcvr.char < 0 {
		Beautyrcvr.char, Beautytoken = Beautylex1(Beautylex, &Beautyrcvr.lval)
	}
	Beautyn += Beautytoken
	if Beautyn < 0 || Beautyn >= BeautyLast {
		goto Beautydefault
	}
	Beautyn = BeautyAct[Beautyn]
	if BeautyChk[Beautyn] == Beautytoken { /* valid shift */
		Beautyrcvr.char = -1
		Beautytoken = -1
		BeautyVAL = Beautyrcvr.lval
		Beautystate = Beautyn
		if Errflag > 0 {
			Errflag--
		}
		goto Beautystack
	}

Beautydefault:
	/* default state action */
	Beautyn = BeautyDef[Beautystate]
	if Beautyn == -2 {
		if Beautyrcvr.char < 0 {
			Beautyrcvr.char, Beautytoken = Beautylex1(Beautylex, &Beautyrcvr.lval)
		}

		/* look through exception table */
		xi := 0
		for {
			if BeautyExca[xi+0] == -1 && BeautyExca[xi+1] == Beautystate {
				break
			}
			xi += 2
		}
		for xi += 2; ; xi += 2 {
			Beautyn = BeautyExca[xi+0]
			if Beautyn < 0 || Beautyn == Beautytoken {
				break
			}
		}
		Beautyn = BeautyExca[xi+1]
		if Beautyn < 0 {
			goto ret0
		}
	}
	if Beautyn == 0 {
		/* error ... attempt to resume parsing */
		switch Errflag {
		case 0: /* brand new error */
			Beautylex.Error(BeautyErrorMessage(Beautystate, Beautytoken))
			Nerrs++
			if BeautyDebug >= 1 {
				__yyfmt__.Printf("%s", BeautyStatname(Beautystate))
				__yyfmt__.Printf(" saw %s\n", BeautyTokname(Beautytoken))
			}
			fallthrough

		case 1, 2: /* incompletely recovered error ... try again */
			Errflag = 3

			/* find a state where "error" is a legal shift action */
			for Beautyp >= 0 {
				Beautyn = BeautyPact[BeautyS[Beautyp].yys] + BeautyErrCode
				if Beautyn >= 0 && Beautyn < BeautyLast {
					Beautystate = BeautyAct[Beautyn] /* simulate a shift of "error" */
					if BeautyChk[Beautystate] == BeautyErrCode {
						goto Beautystack
					}
				}

				/* the current p has no shift on "error", pop stack */
				if BeautyDebug >= 2 {
					__yyfmt__.Printf("error recovery pops state %d\n", BeautyS[Beautyp].yys)
				}
				Beautyp--
			}
			/* there is no state on the stack with an error shift ... abort */
			goto ret1

		case 3: /* no shift yet; clobber input char */
			if BeautyDebug >= 2 {
				__yyfmt__.Printf("error recovery discards %s\n", BeautyTokname(Beautytoken))
			}
			if Beautytoken == BeautyEofCode {
				goto ret1
			}
			Beautyrcvr.char = -1
			Beautytoken = -1
			goto Beautynewstate /* try again in the same state */
		}
	}

	/* reduction by production Beautyn */
	if BeautyDebug >= 2 {
		__yyfmt__.Printf("reduce %v in:\n\t%v\n", Beautyn, BeautyStatname(Beautystate))
	}

	Beautynt := Beautyn
	Beautypt := Beautyp
	_ = Beautypt // guard against "declared and not used"

	Beautyp -= BeautyR2[Beautyn]
	// Beautyp is now the index of $0. Perform the default action. Iff the
	// reduced production is ε, $1 is possibly out of range.
	if Beautyp+1 >= len(BeautyS) {
		nyys := make([]BeautySymType, len(BeautyS)*2)
		copy(nyys, BeautyS)
		BeautyS = nyys
	}
	BeautyVAL = BeautyS[Beautyp+1]

	/* consult goto table to find next state */
	Beautyn = BeautyR1[Beautyn]
	Beautyg := BeautyPgo[Beautyn]
	Beautyj := Beautyg + BeautyS[Beautyp].yys + 1

	if Beautyj >= BeautyLast {
		Beautystate = BeautyAct[Beautyg]
	} else {
		Beautystate = BeautyAct[Beautyj]
		if BeautyChk[Beautystate] != -Beautyn {
			Beautystate = BeautyAct[Beautyg]
		}
	}
	// dummy call; replaced with literal code
	switch Beautynt {

	case 1:
		BeautyDollar = BeautyS[Beautypt-2 : Beautypt+1]
//line parser.go.y:28
		{
			fmt.Println(BeautyDollar[1].val)
			BeautyVAL.val = BeautyDollar[2].val
		}
	case 2:
		BeautyDollar = BeautyS[Beautypt-1 : Beautypt+1]
//line parser.go.y:32
		{
			BeautyVAL.val = BeautyDollar[1].val
		}
	}
	goto Beautystack /* stack new state and value */
}