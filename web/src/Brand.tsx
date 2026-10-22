import logo from "../../logo.png";

export function Brand() {
  return (
    <span className="brand-lockup">
      <img className="brand-logo" src={logo} alt="" width="30" height="30" />
      <span>DByson</span>
    </span>
  );
}
