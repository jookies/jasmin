import { Create, useForm } from "@refinedev/antd";
import { MORouteFields } from "./form";

export const MORouteCreate = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Create saveButtonProps={saveButtonProps}>
      <MORouteFields formProps={formProps} />
    </Create>
  );
};
