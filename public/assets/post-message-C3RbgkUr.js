const i={ClientReady:"isuride.client.ready",ClientRideRequested:"isuride.client.running",SimulatorConfing:"isuride.simulator.config"},t=(e,s)=>{e.postMessage({type:i.ClientReady,payload:s},"*")},n=(e,s)=>{e.postMessage({type:i.ClientRideRequested,payload:s},"*")},a=(e,s)=>{e.postMessage({type:i.SimulatorConfing,payload:s},"*")};export{i as M,n as a,t as b,a as s};