package types

// CyberAccessProgram selects an authorized program for one turn. Authorization
// and model restrictions remain with the app-server provider. Runtime
// support is proved for a direct native Linux app-server executable.
type CyberAccessProgram string

const (
	CyberAccessProgramStandard     CyberAccessProgram = "standard"
	CyberAccessProgramDaybreakBlue CyberAccessProgram = "daybreakBlue"
	CyberAccessProgramDaybreakRed  CyberAccessProgram = "daybreakRed"
)
